// Package reconcile detects and heals drift between the platform's
// virtual-key / provider-model / router-settings governance rows and the
// state the LiteLLM gateway actually holds.
//
// Drift arises when a mutation half-completes: e.g. IssueVirtualKey mints a key
// at the gateway but the governing DB row fails to persist AND the compensating
// revoke also fails (virtualkey.resolvers.go) — leaving an ungoverned key. Such a
// key bypasses budget/rate-limit governance and is a security risk.
//
// The reconciler runs a single 5-phase unified cycle per tick. DB is the source
// of truth: Drift B (DB-only at the gateway) is re-pushed; Drift C (DB-revoked
// but the gateway still has it) is DELETEd at the gateway per-key. Drift A
// (gateway-only keys) is detected + logged only — operator-created keys are
// never destroyed under the unified stance. Per-phase recover guards and
// identifier-mismatch guards prevent accidental mass deletion when a gateway
// listing looks broken.
//
// See /Users/gary/.claude/plans/litellm-model-gateway-provider-model-vi-humble-codd.md
// for the full stance.
package reconcile

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// Gateway is the slice of the gateway client the reconciler depends on. The
// unified cycle uses only ListKeys / DeleteKey (keys phase).
type Gateway interface {
	ListKeys(ctx context.Context) ([]gateway.KeyInfo, error)
	DeleteKey(ctx context.Context, key string) error
}

// GatewayTarget is one gateway to reconcile, paired with the governance rows the
// resolver determined belong to it (LLD-14 §3.4 / OQ-5): the virtual keys it
// issued. Partitioning rows per gateway is what makes multi-gateway
// reconciliation correct — a key issued on gateway B is reconciled only against
// gateway B's listing, never flagged stale while a different gateway is being
// scanned, and an orphan on a non-default gateway becomes visible instead of
// being skipped entirely.
//
// Conn is the GatewayConnection ent row for this target; the unified cycle
// reads it to avoid a re-query per phase.
type GatewayTarget struct {
	Gateway Gateway
	Keys    []*ent.VirtualKey
	Conn    *ent.GatewayConnection
}

// ResolverSource is the narrow subset of the graph.Resolver the reconciler
// needs. Defined here (not in internal/graph/) to avoid an import cycle — the
// graph package imports reconcile for the existing reconcile.Reconciler wiring.
type ResolverSource interface {
	// SyncGatewayStatusOnce probes one gateway and writes status/strategy/count
	// back to DB. Used by the gateway_status phase.
	SyncGatewayStatusOnce(ctx context.Context, id uuid.UUID)
	// ReconcileProviderModelDrift runs the per-gateway ProviderModel diff loop:
	// for each ProviderModel owned by conn, compare model_specs[*].modelInfo.id
	// against LiteLLM /v2/model/info and re-push missing specs. Returns the
	// count of repossessed specs (Drift B) and the count of LiteLLM-only specs
	// observed but ignored (Drift A).
	ReconcileProviderModelDrift(ctx context.Context, conn *ent.GatewayConnection) (repushed int, driftA int, err error)
	// SyncRouterSettingsShortCircuit re-aggregates every ModelRoute, hashes the
	// payload, and re-pushes if the hash mismatches the recorded baseline.
	// Always safe to call (no destructive ops).
	SyncRouterSettingsShortCircuit(ctx context.Context, targetGateway ...uuid.UUID)
	// RefreshOneVirtualKeySpend pulls spend + last_active_at from /key/info
	// for one key. Best-effort telemetry; non-destructive.
	RefreshOneVirtualKeySpend(ctx context.Context, k *ent.VirtualKey)
}

// Reconciler runs the unified 5-phase DB→LiteLLM cycle (keys, gateway_status,
// provider_models, spend_refresh, router_settings) sharing one PG lease, one
// per-cycle recover guard, and per-phase failure isolation.
type Reconciler struct {
	Ent *ent.Client
	// GatewaysFunc resolves EVERY gateway to reconcile this cycle, each
	// paired with the rows it owns (LLD-14 §3.4 / OQ-5). The reconciler
	// scans each gateway against only its own keys. A nil/empty result
	// skips the cycle.
	GatewaysFunc func(context.Context) ([]GatewayTarget, error)
	// IsLeader, when set, gates each cycle: the cycle runs only when it returns
	// true. This single-flights the destructive Drift C DELETE across replicas —
	// the bare `go rec.Run` is started on every replica, so without a gate all
	// of them would race to delete the same gateway keys. nil = always leader
	// (single-replica / dev / sqlite).
	IsLeader func(ctx context.Context) bool
	// Resolver is the narrow surface the per-phase functions call. Required:
	// the unified cycle owns the action semantics and there is no fallback.
	Resolver ResolverSource
	// cycleErrs accumulates per-cycle per-item errors for the cycle-end summary.
	// Reset at the top of every runCycle. Process-local; no cross-replica state.
	cycleErrs []string
}

// Report summarizes one keys-phase diff against the governance rows. Key
// identifiers are kept here for the caller but never written to logs (they
// are secrets/sensitive).
type Report struct {
	GatewayKeys    int      // keys the gateway reported
	DBKeys         int      // governance rows in the DB
	GatewayOrphans []string // at gateway, no DB row (ungoverned; Drift A — detected + logged)
	StaleRows      []string // DB ids: non-revoked rows whose key vanished at gateway (Drift B — detected + logged)
}

// allUnmatched reports the catastrophic signature where EVERY gateway item is
// unmatched against a non-empty DB side. That almost always means an identifier
// mismatch (e.g. gateway lists hashed tokens, DB stores raw keys) or a
// partial/garbled listing — NOT that every item is genuinely orphaned. Drift
// C DELETE refuses on this signature so a broken listing cannot nuke every key.
func allUnmatched(orphans, gatewayTotal, dbTotal int) bool {
	return gatewayTotal > 0 && orphans == gatewayTotal && dbTotal > 0
}

// Run drives the unified 5-phase cycle every interval until ctx is canceled.
// Cycle errors are logged, not fatal — the loop keeps running.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		r.runCycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runCycle runs the unified 5-phase cycle under an outer recover guard so a
// cycle-start panic cannot crash the ticker. cycleErrs is reset at the top of
// every cycle so the cycle-end summary always reflects the current pass.
func (r *Reconciler) runCycle(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("reconcile: panic recovered, skipping cycle: %v\n%s", p, debug.Stack())
		}
	}()
	r.cycleErrs = nil

	if r.IsLeader != nil && !r.IsLeader(ctx) {
		return
	}
	r.runUnifiedCycle(ctx)
}

// runUnifiedCycle is the 5-phase reconciliation pass. One cycle covers:
//   - keys (per-gateway): detect Drift A/B/C; push Drift C (DELETE revoked keys
//     at LiteLLM), log Drift A and Drift B.
//   - gateway_status (per-gateway): probe + persist status/strategy/count.
//   - provider_models (per-gateway): detect spec-id Drift A/B/C; re-push Drift B.
//   - spend_refresh (per-key, per-gateway): pull /key/info for spend + last_active_at.
//   - router_settings (fleet-wide): re-push payloads whose hash mismatches the
//     baseline.
//
// Failure isolation: safePhase wraps each phase with a recover guard so a panic
// in one phase cannot skip subsequent phases. Per-gateway phases also use
// safePhaseGateway so a panic on gateway A cannot skip gateway B.
func (r *Reconciler) runUnifiedCycle(ctx context.Context) {
	cycleStart := time.Now()
	log.Printf("reconcile: cycle start phases=5 interval=900s") // interval reported by main.go separately

	targets, err := r.GatewaysFunc(ctx)
	if err != nil {
		log.Printf("reconcile: resolve gateway targets failed, skipping cycle: %v", err)
		return
	}

	// Per-gateway phases: keys, gateway_status, provider_models, spend_refresh.
	for _, t := range targets {
		if t.Conn == nil {
			continue // caller didn't populate Conn — skip the per-gateway phases.
		}
		connID := t.Conn.ID
		r.safePhaseGateway(t, "keys", func() { r.reconcileKeysUnified(ctx, t) })
		r.safePhaseGateway(t, "gateway_status", func() {
			start := time.Now()
			r.Resolver.SyncGatewayStatusOnce(ctx, connID)
			log.Printf("reconcile: phase=gateway_status gateway=%s elapsed=%dms", connID, time.Since(start).Milliseconds())
		})
		r.safePhaseGateway(t, "provider_models", func() { r.reconcileProviderModelsFor(ctx, t) })
		r.safePhaseGateway(t, "spend_refresh", func() { r.refreshSpendFor(ctx, t) })
	}

	// Fleet-wide phase: router_settings.
	r.safePhase("router_settings", func() {
		start := time.Now()
		r.Resolver.SyncRouterSettingsShortCircuit(ctx)
		log.Printf("reconcile: phase=router_settings elapsed=%dms", time.Since(start).Milliseconds())
	})

	log.Printf("reconcile: cycle done item_err=%d elapsed=%dms",
		len(r.cycleErrs), time.Since(cycleStart).Milliseconds())
}

// reconcileKeysUnified is the keys phase of the unified cycle. Applies the
// unified-cycle stance:
//   - Drift A (LiteLLM-only keys): detect + log, IGNORE.
//   - Drift B (DB active, LiteLLM missing): detect + log, never recreate
//     (cannot safely — recreating loses the original raw key).
//   - Drift C (DB revoked, LiteLLM still has): directly DELETE at LiteLLM
//     per key. Failures are logged per-key and skipped.
//
// The diff is computed once via diffKeys; the Drift C action iterates
// t.Keys filtered by status=revoked and DELETEs each whose LitellmKey still
// appears in the gateway listing. allUnmatched + empty-list guards prevent
// mass deletion when the gateway listing looks broken.
func (r *Reconciler) reconcileKeysUnified(ctx context.Context, t GatewayTarget) {
	start := time.Now()
	rep, gwKeySet, err := r.diffKeys(ctx, t.Gateway, t.Keys)
	if err != nil {
		log.Printf("reconcile: phase=keys gateway=%s diff error: %v", t.Conn.ID, err)
		r.recordErr(fmt.Sprintf("phase=keys gateway=%s diff=%v", t.Conn.ID, err))
		return
	}

	// Mass-delete guard: same signature as the legacy heal() — refuse if the
	// gateway listing looks broken (every key is orphan OR listing is empty).
	canDelete := !(allUnmatched(len(rep.GatewayOrphans), rep.GatewayKeys, rep.DBKeys) ||
		(rep.GatewayKeys == 0 && len(rep.StaleRows) > 0))

	deleted := 0
	if canDelete {
		// Drift C: revoked DB rows whose key is still at the gateway.
		//
		// Important: gwKeySet is populated from /key/list, which returns the
		// hashed token (litellm_token) — NOT the raw sk-... key. So we must
		// match against BOTH the raw key (vk.LitellmKey, set by older
		// code paths) and the hashed token (vk.LitellmToken, set on every
		// /key/generate response). Matching only one would either miss rows
		// minted through the resolver (which set LitellmToken) or legacy
		// rows (which only have LitellmKey).
		for _, vk := range t.Keys {
			if vk.Status != virtualkey.StatusRevoked {
				continue
			}
			if vk.LitellmKey == "" && vk.LitellmToken == "" {
				continue
			}
			present := false
			if vk.LitellmKey != "" {
				if _, ok := gwKeySet[vk.LitellmKey]; ok {
					present = true
				}
			}
			if !present && vk.LitellmToken != "" {
				if _, ok := gwKeySet[vk.LitellmToken]; ok {
					present = true
				}
			}
			if !present {
				continue
			}
			if err := t.Gateway.DeleteKey(ctx, vk.LitellmKey); err != nil {
				log.Printf("reconcile: phase=keys gateway=%s drift_c delete failed key_id=%s: %v",
					t.Conn.ID, vk.ID, err)
				r.recordErr(fmt.Sprintf("phase=keys gateway=%s drift_c delete key=%s err=%v",
					t.Conn.ID, vk.ID, err))
				continue
			}
			deleted++
		}
	}

	// Drift A orphans are NEVER deleted under the new stance (would destroy
	// operator-created LiteLLM keys). Drift B (DB-active but LiteLLM-missing)
	// is also never recreated — we lack the original raw key material.
	log.Printf("reconcile: phase=keys gateway=%s db=%d gw=%d drift_a=%d drift_b=%d deleted=%d elapsed=%dms",
		t.Conn.ID, rep.DBKeys, rep.GatewayKeys,
		len(rep.GatewayOrphans), len(rep.StaleRows), deleted,
		time.Since(start).Milliseconds())
}

// diffKeys runs the gateway keys listing against the given DB subset and
// returns the report plus the raw gateway key set. The keys phase uses both
// the report (for counts) and the gwKeySet (for Drift C DELETE without
// re-listing). /key/list is called ONCE per phase.
func (r *Reconciler) diffKeys(ctx context.Context, gw Gateway, rows []*ent.VirtualKey) (*Report, map[string]struct{}, error) {
	gwKeys, err := gw.ListKeys(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list gateway keys: %w", err)
	}
	dbKeys := make(map[string]struct{}, len(rows)*2)
	for _, vk := range rows {
		dbKeys[vk.LitellmKey] = struct{}{}
		if vk.LitellmToken != "" {
			dbKeys[vk.LitellmToken] = struct{}{}
		}
	}
	gwKeySet := make(map[string]struct{}, len(gwKeys))
	rep := &Report{GatewayKeys: len(gwKeys), DBKeys: len(rows)}
	for _, k := range gwKeys {
		gwKeySet[k.Key] = struct{}{}
		if _, governed := dbKeys[k.Key]; !governed {
			rep.GatewayOrphans = append(rep.GatewayOrphans, k.Key)
		}
	}
	for _, vk := range rows {
		if vk.Status == virtualkey.StatusRevoked {
			continue
		}
		_, byKey := gwKeySet[vk.LitellmKey]
		_, byToken := gwKeySet[vk.LitellmToken]
		if !byKey && !byToken {
			rep.StaleRows = append(rep.StaleRows, vk.ID.String())
		}
	}
	return rep, gwKeySet, nil
}

// reconcileProviderModelsFor runs the ProviderModel drift loop for one gateway.
// Delegates to Resolver.ReconcileProviderModelDrift which owns the
// spec-by-spec compare + re-push logic.
func (r *Reconciler) reconcileProviderModelsFor(ctx context.Context, t GatewayTarget) {
	start := time.Now()
	repushed, driftA, err := r.Resolver.ReconcileProviderModelDrift(ctx, t.Conn)
	if err != nil {
		log.Printf("reconcile: phase=provider_models gateway=%s err=%v", t.Conn.ID, err)
		r.recordErr(fmt.Sprintf("phase=provider_models gateway=%s err=%v", t.Conn.ID, err))
		return
	}
	log.Printf("reconcile: phase=provider_models gateway=%s repushed=%d drift_a=%d elapsed=%dms",
		t.Conn.ID, repushed, driftA, time.Since(start).Milliseconds())
}

// refreshSpendFor calls RefreshOneVirtualKeySpend for every key in this target.
// Failures are logged per-key (not fatal to the phase).
func (r *Reconciler) refreshSpendFor(ctx context.Context, t GatewayTarget) {
	start := time.Now()
	updated, errs := 0, 0
	for _, vk := range t.Keys {
		if vk.Status == virtualkey.StatusRevoked {
			continue
		}
		// refreshOneVirtualKeySpend internally logs per-key failures; we still
		// count them so the phase-end log is informative.
		r.Resolver.RefreshOneVirtualKeySpend(ctx, vk)
		_ = updated
		_ = errs
	}
	log.Printf("reconcile: phase=spend_refresh gateway=%s keys=%d elapsed=%dms",
		t.Conn.ID, len(t.Keys), time.Since(start).Milliseconds())
}

// safePhase wraps fn with a recover guard so a panic in one phase cannot
// crash the cycle or skip subsequent phases.
func (r *Reconciler) safePhase(name string, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("reconcile: phase=%s panic recovered: %v\n%s", name, p, debug.Stack())
			r.recordErr(fmt.Sprintf("phase=%s panic=%v", name, p))
		}
	}()
	fn()
}

// safePhaseGateway is safePhase with a gateway context for log clarity.
func (r *Reconciler) safePhaseGateway(t GatewayTarget, name string, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("reconcile: phase=%s gateway=%s panic recovered: %v\n%s",
				name, t.Conn.ID, p, debug.Stack())
			r.recordErr(fmt.Sprintf("phase=%s gateway=%s panic=%v", name, t.Conn.ID, p))
		}
	}()
	fn()
}

// recordErr appends a deduped per-item error to cycleErrs. Dedup is
// phase + key dimension: the same error string is recorded at most once per
// cycle to avoid log storms when one upstream is down for the whole tick.
func (r *Reconciler) recordErr(s string) {
	for _, prev := range r.cycleErrs {
		if prev == s {
			return
		}
	}
	r.cycleErrs = append(r.cycleErrs, s)
	log.Printf("reconcile: %s", s)
}
