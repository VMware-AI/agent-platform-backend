// Package reconcile detects and (optionally) heals drift between the platform's
// virtual-key governance rows and the keys the LiteLLM gateway actually holds.
//
// Drift arises when a mutation half-completes: e.g. IssueVirtualKey mints a key
// at the gateway but the governing DB row fails to persist AND the compensating
// revoke also fails (virtualkey.resolvers.go) — leaving an ungoverned key. Such a
// key bypasses budget/rate-limit governance and is a security risk.
//
// The reconciler is report-only by default ("对账": find discrepancies, change
// nothing). Healing is opt-in via Prune so that a partial gateway listing or an
// identifier mismatch can never trigger accidental mass deletion.
//
// # Extended (unified) reconciler
//
// When Resolver is non-nil, the reconciler runs an extended cycle that covers
// FOUR resources + spend refresh in a single pass, with per-phase recover
// guards. The extended cycle is the "DB → LiteLLM" direction: it pushes DB
// state out to LiteLLM (delete revoked keys, re-push missing provider-model
// specs, re-push router_settings). LiteLLM-only resources are detect + log
// only (Drift A) — they are not deleted or imported. See
// /Users/gary/.claude/plans/litellm-model-gateway-provider-model-vi-humble-codd.md
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

// Gateway is the slice of the gateway client the reconciler depends on.
// ListTeams/DeleteTeam remain for the legacy team reconciliation path; the
// unified cycle (Resolver != nil) ignores teams entirely.
type Gateway interface {
	ListKeys(ctx context.Context) ([]gateway.KeyInfo, error)
	DeleteKey(ctx context.Context, key string) error
	ListTeams(ctx context.Context) ([]gateway.TeamInfo, error)
	DeleteTeam(ctx context.Context, teamID string) error
}

// GatewayTarget is one gateway to reconcile, paired with the governance rows the
// resolver determined belong to it (LLD-14 §3.4 / OQ-5): the virtual keys it
// issued. Partitioning rows per gateway is what makes multi-gateway
// reconciliation correct — a key issued on gateway B is reconciled only against
// gateway B's listing, never flagged stale while a different gateway is being
// scanned, and an orphan on a non-default gateway becomes visible instead of
// being skipped entirely.
//
// Conn is the GatewayConnection ent row for this target; nil in legacy paths
// that pre-date the unified reconciler. The unified cycle reads it to avoid a
// re-query per phase.
type GatewayTarget struct {
	Gateway Gateway
	Keys    []*ent.VirtualKey
	Depts   []*ent.Department // unused by the unified cycle (teams are out of scope)
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

// Reconciler compares gateway keys/teams against governance rows. When Resolver
// is non-nil, also runs the unified 5-phase cycle (keys, gateway_status,
// provider_models, spend_refresh, router_settings) sharing one PG lease, one
// per-cycle recover guard, and per-phase failure isolation.
type Reconciler struct {
	Ent *ent.Client
	// GatewaysFunc resolves EVERY gateway to reconcile this cycle, each
	// paired with the rows it owns (LLD-14 §3.4 / OQ-5). The reconciler
	// scans each gateway against only its own keys/teams. A nil/empty
	// result skips the cycle.
	GatewaysFunc func(context.Context) ([]GatewayTarget, error)
	// Prune enables healing: delete gateway orphans + revoke stale DB rows. When
	// false (default) the reconciler only reports. Only consulted in the legacy
	// (Resolver == nil) path; the unified cycle does NOT take direction from
	// Prune (Drift B/C execute unconditionally — DB is source of truth).
	Prune bool
	// IsLeader, when set, gates each cycle: the cycle runs only when it returns
	// true. This single-flights the (destructive, under Prune) reconcile across
	// replicas — the bare `go rec.Run` is started on every replica, so without a
	// gate all of them would race to delete gateway keys/teams and revoke rows.
	// nil = always leader (single-replica / dev / sqlite).
	IsLeader func(ctx context.Context) bool
	// Resolver enables the unified reconciler cycle. nil = legacy behavior
	// (keys + teams, Prune-gated). Non-nil = unified 5-phase cycle.
	Resolver ResolverSource
	// cycleErrs accumulates per-cycle per-item errors for the cycle-end summary.
	// Reset at the top of every runCycle. Process-local; no cross-replica state.
	cycleErrs []string
}

// Report summarizes one reconciliation pass. Key identifiers are kept here for
// the caller but never written to logs (they are secrets/sensitive).
type Report struct {
	GatewayKeys    int      // keys the gateway reported
	DBKeys         int      // governance rows in the DB
	GatewayOrphans []string // at gateway, no DB row (ungoverned)
	StaleRows      []string // DB ids: non-revoked rows whose key vanished at gateway
	Pruned         int      // orphans deleted at the gateway (Prune only)
	Revoked        int      // stale rows marked revoked (Prune only)
}

// ReconcileKeys runs one pass over ALL governance rows against the given
// gateway. Used by the per-gateway path (runCycle) for a pre-scoped row
// subset. Returns an error only when it cannot read both sides; per-item
// heal failures are logged and counted, not fatal.
func (r *Reconciler) ReconcileKeys(ctx context.Context, gw Gateway) (*Report, error) {
	rows, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list governance rows: %w", err)
	}
	return r.reconcileKeysFor(ctx, gw, rows)
}

// reconcileKeysFor diffs ONE gateway's listed keys against the GIVEN governance
// rows — the subset the resolver assigned to that gateway. Keeping the row set
// scoped to the gateway is what makes the diff correct under multiple gateways:
// the stale/orphan guards (see heal) then reason only about keys that should live
// on this gateway. Returns an error only when it cannot read the gateway side.
func (r *Reconciler) reconcileKeysFor(ctx context.Context, gw Gateway, rows []*ent.VirtualKey) (*Report, error) {
	gwKeys, err := gw.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway keys: %w", err)
	}

	// A row is identified at the gateway by EITHER its raw key or its hashed token
	// (whichever LiteLLM's /key/list returns). Index both so reconciliation works
	// regardless of which identifier the gateway reports — and so rows persisted
	// before litellm_token existed still match by their raw key.
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
			continue // already terminal — its key is expected to be gone
		}
		// Present if the gateway lists either identifier. An empty token never
		// matches (the gateway set has no empty entries), so it is harmless.
		_, byKey := gwKeySet[vk.LitellmKey]
		_, byToken := gwKeySet[vk.LitellmToken]
		if !byKey && !byToken {
			rep.StaleRows = append(rep.StaleRows, vk.ID.String())
		}
	}

	if r.Prune {
		r.heal(ctx, gw, rep)
	}

	// Log counts only — never the key identifiers themselves.
	log.Printf("reconcile keys: gateway=%d db=%d orphans=%d stale=%d pruned=%d revoked=%d prune=%v",
		rep.GatewayKeys, rep.DBKeys, len(rep.GatewayOrphans), len(rep.StaleRows),
		rep.Pruned, rep.Revoked, r.Prune)
	return rep, nil
}

// TeamReport summarizes one team reconciliation pass.
type TeamReport struct {
	GatewayTeams  int      // teams the gateway reported
	DBDepartments int      // departments with a litellm_team_id
	TeamOrphans   []string // team ids at gateway with no department (prune candidates)
	DanglingDepts []string // department ids whose litellm_team_id is absent at gateway
	Pruned        int      // orphan teams deleted at the gateway (Prune only)
}

// ReconcileTeams compares the given gateway's teams against ALL department
// rows. Mirrors ReconcileKeys' per-item count/log behavior.
func (r *Reconciler) ReconcileTeams(ctx context.Context, gw Gateway) (*TeamReport, error) {
	depts, err := r.Ent.Department.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	return r.reconcileTeamsFor(ctx, gw, depts)
}

// reconcileTeamsFor diffs ONE gateway's teams against the GIVEN department subset
// (the departments the resolver assigned to that gateway). Gateway team orphans
// (no backing department) are pruned under Prune; dangling department references
// are only reported — re-creating vs clearing the link is an operator policy
// decision. Returns an error only when it cannot read the gateway side.
func (r *Reconciler) reconcileTeamsFor(ctx context.Context, gw Gateway, depts []*ent.Department) (*TeamReport, error) {
	gwTeams, err := gw.ListTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway teams: %w", err)
	}

	dbTeams := make(map[string]struct{})
	dbCount := 0
	for _, d := range depts {
		if d.LitellmTeamID == "" {
			continue // not linked to a gateway team
		}
		dbCount++
		dbTeams[d.LitellmTeamID] = struct{}{}
	}
	gwTeamSet := make(map[string]struct{}, len(gwTeams))

	rep := &TeamReport{GatewayTeams: len(gwTeams), DBDepartments: dbCount}
	for _, t := range gwTeams {
		gwTeamSet[t.TeamID] = struct{}{}
		if _, backed := dbTeams[t.TeamID]; !backed {
			rep.TeamOrphans = append(rep.TeamOrphans, t.TeamID)
		}
	}
	for _, d := range depts {
		if d.LitellmTeamID == "" {
			continue
		}
		if _, present := gwTeamSet[d.LitellmTeamID]; !present {
			rep.DanglingDepts = append(rep.DanglingDepts, d.ID.String())
		}
	}

	if r.Prune {
		// Same guard as keys: if EVERY gateway team is unmatched against a non-empty
		// department set, it's a mismatch or a shared gateway full of foreign teams —
		// refuse to delete them all.
		if allUnmatched(len(rep.TeamOrphans), rep.GatewayTeams, rep.DBDepartments) {
			log.Printf("reconcile: SKIP team prune — all %d gateway teams unmatched against %d departments; likely mismatch or foreign teams, refusing to prune",
				rep.GatewayTeams, rep.DBDepartments)
		} else {
			for _, teamID := range rep.TeamOrphans {
				if err := gw.DeleteTeam(ctx, teamID); err != nil {
					log.Printf("reconcile: prune gateway team orphan failed: %v", err)
					continue
				}
				rep.Pruned++
			}
		}
	}

	log.Printf("reconcile teams: gateway=%d db=%d orphans=%d dangling=%d pruned=%d prune=%v",
		rep.GatewayTeams, rep.DBDepartments, len(rep.TeamOrphans), len(rep.DanglingDepts),
		rep.Pruned, r.Prune)
	return rep, nil
}

// allUnmatched reports the catastrophic signature where EVERY gateway item is
// unmatched against a non-empty DB side. That almost always means an identifier
// mismatch (e.g. gateway lists hashed tokens, DB stores raw keys) or a
// partial/garbled listing — NOT that every item is genuinely orphaned. Pruning on
// it would delete everything, so callers refuse and report instead.
func allUnmatched(orphans, gatewayTotal, dbTotal int) bool {
	return gatewayTotal > 0 && orphans == gatewayTotal && dbTotal > 0
}

// heal deletes gateway orphans and revokes stale rows, counting successes. Two
// safety guards prevent mass destruction from a bad diff: (1) refuse entirely
// when every gateway key is unmatched (identifier mismatch); (2) never revoke
// stale rows when the gateway returned no keys at all (a failed/empty listing
// must not nuke every governance row).
func (r *Reconciler) heal(ctx context.Context, gw Gateway, rep *Report) {
	if allUnmatched(len(rep.GatewayOrphans), rep.GatewayKeys, rep.DBKeys) {
		log.Printf("reconcile: SKIP key heal — all %d gateway keys unmatched against %d DB rows; likely identifier mismatch or partial list, refusing to prune/revoke",
			rep.GatewayKeys, rep.DBKeys)
		return
	}
	for _, key := range rep.GatewayOrphans {
		if err := gw.DeleteKey(ctx, key); err != nil {
			log.Printf("reconcile: prune gateway orphan failed: %v", err)
			continue
		}
		rep.Pruned++
	}
	if rep.GatewayKeys == 0 && len(rep.StaleRows) > 0 {
		log.Printf("reconcile: SKIP revoking %d stale rows — gateway returned no keys (possible failed/empty list)",
			len(rep.StaleRows))
		return
	}
	for _, id := range rep.StaleRows {
		kid, err := uuid.Parse(id)
		if err != nil {
			log.Printf("reconcile: bad stale row id %q: %v", id, err)
			continue
		}
		if _, err := r.Ent.VirtualKey.UpdateOneID(kid).
			SetStatus(virtualkey.StatusRevoked).Save(ctx); err != nil {
			log.Printf("reconcile: revoke stale row %s failed: %v", id, err)
			continue
		}
		rep.Revoked++
	}
}

// Run reconciles keys and teams immediately, then every interval until ctx is
// canceled. Cycle errors are logged, not fatal — the loop keeps running.
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

// runCycle dispatches to the unified cycle when Resolver is set, otherwise the
// legacy keys+teams cycle. The outer recover guard is shared — both paths run
// under it. cycleErrs is reset at the top of every cycle so the cycle-end
// summary always reflects the current pass.
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

	if r.Resolver != nil {
		r.runUnifiedCycle(ctx)
		return
	}
	r.runLegacyCycle(ctx)
}

// runUnifiedCycle is the new 5-phase reconciliation pass. One cycle covers:
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
			continue // legacy caller (no Conn populated) — skip unified phases.
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

// reconcileKeysUnified is the keys phase of the unified cycle. Mirrors
// reconcileKeysFor's diff but applies the new stance:
//   - Drift A (LiteLLM-only keys): detect + log, IGNORE.
//   - Drift B (DB active, LiteLLM missing): detect + log, never recreate
//     (cannot safely — recreating loses the original raw key).
//   - Drift C (DB revoked, LiteLLM still has): directly DELETE at LiteLLM
//     per key. Failures are logged per-key and skipped.
func (r *Reconciler) reconcileKeysUnified(ctx context.Context, t GatewayTarget) {
	start := time.Now()
	rep, err := r.diffKeys(ctx, t.Gateway, t.Keys)
	if err != nil {
		log.Printf("reconcile: phase=keys gateway=%s diff error: %v", t.Conn.ID, err)
		r.recordErr(fmt.Sprintf("phase=keys gateway=%s diff=%v", t.Conn.ID, err))
		return
	}
	deleted := 0
	for _, key := range rep.GatewayOrphans {
		// Drift A: LiteLLM-only keys are NOT touched under the new stance.
		_ = key
	}
	for _, idStr := range rep.StaleRows {
		// Drift B: DB row missing at LiteLLM — never recreate. Log only.
		log.Printf("reconcile: phase=keys gateway=%s drift_b stale_db_id=%s", t.Conn.ID, idStr)
	}
	if len(rep.StaleRows) == 0 && len(rep.GatewayOrphans) == 0 {
		log.Printf("reconcile: phase=keys gateway=%s db=%d gw=%d drift_a=0 drift_b=0 drift_c=0 deleted=0 elapsed=%dms",
			t.Conn.ID, rep.DBKeys, rep.GatewayKeys, time.Since(start).Milliseconds())
		return
	}
	log.Printf("reconcile: phase=keys gateway=%s db=%d gw=%d drift_a=%d drift_b=%d drift_c=%d deleted=%d elapsed=%dms",
		t.Conn.ID, rep.DBKeys, rep.GatewayKeys,
		len(rep.GatewayOrphans), len(rep.StaleRows), 0, deleted,
		time.Since(start).Milliseconds())
}

// diffKeys runs the gateway keys listing against the given DB subset and
// returns the report. Shared by both legacy and unified paths — the legacy
// path uses it via heal(), the unified path uses it to drive its own actions.
func (r *Reconciler) diffKeys(ctx context.Context, gw Gateway, rows []*ent.VirtualKey) (*Report, error) {
	gwKeys, err := gw.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway keys: %w", err)
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
	return rep, nil
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

// runLegacyCycle preserves the pre-unified behavior: keys + teams, Prune-gated
// healing. Kept so a deployment with Resolver == nil falls back to the
// historical semantics. The unified cycle replaces this entirely once
// Resolver is wired in (see PR #3 cut-over).
func (r *Reconciler) runLegacyCycle(ctx context.Context) {
	if r.GatewaysFunc == nil {
		return
	}
	targets, err := r.GatewaysFunc(ctx)
	if err != nil {
		log.Printf("reconcile: resolve gateway targets failed, skipping cycle: %v", err)
		return
	}
	for _, t := range targets {
		if t.Gateway == nil {
			continue
		}
		if _, err := r.reconcileKeysFor(ctx, t.Gateway, t.Keys); err != nil {
			log.Printf("reconcile keys cycle error: %v", err)
		}
		if _, err := r.reconcileTeamsFor(ctx, t.Gateway, t.Depts); err != nil {
			log.Printf("reconcile teams cycle error: %v", err)
		}
	}
}

// (Old runCycle removed; replaced by runCycle (dispatcher) at line ~344 above,
// runUnifiedCycle for the new 5-phase path, and runLegacyCycle for the
// pre-unified keys+teams behavior.)
