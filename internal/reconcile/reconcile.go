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
type Gateway interface {
	ListKeys(ctx context.Context) ([]gateway.KeyInfo, error)
	DeleteKey(ctx context.Context, key string) error
	ListTeams(ctx context.Context) ([]gateway.TeamInfo, error)
	DeleteTeam(ctx context.Context, teamID string) error
}

// GatewayTarget is one gateway to reconcile, paired with the governance rows the
// resolver determined belong to it (LLD-14 §3.4 / OQ-5): the virtual keys it
// issued and the departments it backs. Partitioning rows per gateway is what makes
// multi-gateway reconciliation correct — a key issued on gateway B is reconciled
// only against gateway B's listing, never flagged stale (and wrongly revoked under
// Prune) while a different gateway is being scanned, and an orphan on a non-default
// gateway becomes visible instead of being skipped entirely.
type GatewayTarget struct {
	Gateway Gateway
	Keys    []*ent.VirtualKey
	Depts   []*ent.Department
}

// Reconciler compares gateway keys/teams against governance rows.
type Reconciler struct {
	Ent     *ent.Client
	Gateway Gateway
	// GatewaysFunc, when set, resolves EVERY gateway to reconcile this cycle, each
	// paired with the rows it owns (LLD-14 §3.4 / OQ-5). It replaces the old
	// single-gateway resolver: instead of scanning one default gateway against all
	// rows, the reconciler scans each gateway against only its own keys/teams. A
	// nil/empty result skips the cycle. When set, it takes precedence over the
	// legacy r.Gateway path (which remains for the single-gateway / injected-fake
	// case).
	GatewaysFunc func(context.Context) ([]GatewayTarget, error)
	// Prune enables healing: delete gateway orphans + revoke stale DB rows. When
	// false (default) the reconciler only reports.
	Prune bool
	// IsLeader, when set, gates each cycle: the cycle runs only when it returns
	// true. This single-flights the (destructive, under Prune) reconcile across
	// replicas — the bare `go rec.Run` is started on every replica, so without a
	// gate all of them would race to delete gateway keys/teams and revoke rows.
	// nil = always leader (single-replica / dev / sqlite).
	IsLeader func(ctx context.Context) bool
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

// ReconcileKeys runs one pass over ALL governance rows against r.Gateway. It is
// the single-gateway/legacy entry point (also used by tests); the multi-gateway
// path (runCycle + GatewaysFunc) calls reconcileKeysFor per gateway with a
// pre-scoped row subset instead. Returns an error only when it cannot read both
// sides; per-item heal failures are logged and counted, not fatal.
func (r *Reconciler) ReconcileKeys(ctx context.Context) (*Report, error) {
	rows, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list governance rows: %w", err)
	}
	return r.reconcileKeysFor(ctx, r.Gateway, rows)
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
	// ProtectedOrphans are orphan team ids that were NOT pruned because they still
	// own active (non-revoked) virtual keys — deleting the team would cascade-delete
	// those keys at litellm and strand live agents (#81). Reported so an operator can
	// heal the underlying key/team bucketing asymmetry instead of the reconciler
	// silently leaking the gateway team.
	ProtectedOrphans []string
}

// ReconcileTeams compares r.Gateway's teams against ALL department rows. Mirrors
// ReconcileKeys: the single-gateway/legacy entry point; the multi-gateway path
// calls reconcileTeamsFor per gateway with a pre-scoped department subset. The
// prune-guard set (team ids that still own active keys) is derived from ALL
// virtual keys here, matching the all-rows department scope.
func (r *Reconciler) ReconcileTeams(ctx context.Context) (*TeamReport, error) {
	depts, err := r.Ent.Department.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	keys, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list governance rows: %w", err)
	}
	return r.reconcileTeamsFor(ctx, r.Gateway, depts, activeKeyTeamIDs(keys))
}

// reconcileTeamsFor diffs ONE gateway's teams against the GIVEN department subset
// (the departments the resolver assigned to that gateway). Gateway team orphans
// (no backing department) are pruned under Prune; dangling department references
// are only reported — re-creating vs clearing the link is an operator policy
// decision. Returns an error only when it cannot read the gateway side.
//
// activeKeyTeams is the set of team ids that still own at least one active
// (non-revoked) virtual key on THIS gateway. An orphan team in that set is never
// pruned regardless of Prune: because virtual keys are bucketed by their issuing
// gateway while departments are bucketed by their current binding, a default-
// gateway switch can leave a team on its old gateway looking orphaned (no backing
// department in this bucket) even though its keys still live and bill here.
// Pruning it would cascade-delete those keys and strand live agents (#81), so it
// is surfaced as a ProtectedOrphan instead.
func (r *Reconciler) reconcileTeamsFor(ctx context.Context, gw Gateway, depts []*ent.Department, activeKeyTeams map[string]struct{}) (*TeamReport, error) {
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
				// Safety net (#81): never prune a team that still owns active keys on
				// this gateway. DeleteTeam cascades at litellm and would revoke those
				// still-billing keys, cutting off live agents. This can happen when a
				// department re-binds to another gateway (or the default switches) but
				// its keys — bucketed by issuing gateway — remain here; the team then
				// looks orphaned in this bucket while its keys are very much alive.
				if _, hasActive := activeKeyTeams[teamID]; hasActive {
					rep.ProtectedOrphans = append(rep.ProtectedOrphans, teamID)
					log.Printf("reconcile: SKIP pruning gateway team orphan — it still owns active virtual keys on this gateway (key/team bucketing asymmetry, #81); heal the binding instead")
					continue
				}
				if err := gw.DeleteTeam(ctx, teamID); err != nil {
					log.Printf("reconcile: prune gateway team orphan failed: %v", err)
					continue
				}
				rep.Pruned++
			}
		}
	}

	log.Printf("reconcile teams: gateway=%d db=%d orphans=%d dangling=%d pruned=%d protected=%d prune=%v",
		rep.GatewayTeams, rep.DBDepartments, len(rep.TeamOrphans), len(rep.DanglingDepts),
		rep.Pruned, len(rep.ProtectedOrphans), r.Prune)
	return rep, nil
}

// activeKeyTeamIDs collects the team ids (VirtualKey.team_id == department id ==
// litellm team id, per LLD-13 §3.3) that still own at least one active
// (non-revoked) key. reconcileTeamsFor uses it as a prune-guard so a team with
// live keys is never cascade-deleted (#81). A key with an empty team id (a
// user-level key on the default gateway with no team) contributes nothing.
func activeKeyTeamIDs(keys []*ent.VirtualKey) map[string]struct{} {
	teams := make(map[string]struct{})
	for _, vk := range keys {
		if vk.Status == virtualkey.StatusRevoked {
			continue // terminal — its key is expected to be gone, protects nothing
		}
		if vk.TeamID == "" {
			continue
		}
		teams[vk.TeamID] = struct{}{}
	}
	return teams
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

// runCycle executes one reconciliation pass under a recover guard. The
// reconciler runs as a bare `go rec.Run` directly under main() — not under
// net/http, the only thing in this process that auto-recovers panics — so an
// unrecovered panic on the gateway-client / ent path would crash the entire
// control plane (GraphQL API included). Recovering here logs the stack and lets
// the ticker continue: one bad cycle never takes down the process.
func (r *Reconciler) runCycle(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("reconcile: panic recovered, skipping cycle: %v\n%s", p, debug.Stack())
		}
	}()
	// Single-flight across replicas: only the elected leader reconciles, so the
	// destructive prune isn't duplicated. nil gate = always run (dev/single).
	if r.IsLeader != nil && !r.IsLeader(ctx) {
		return
	}
	// Multi-gateway path (production): reconcile EVERY configured gateway against
	// only the rows the resolver assigned to it (LLD-14 §3.4 / OQ-5). Per-gateway
	// failures are logged, not fatal — one bad gateway never blocks the others.
	if r.GatewaysFunc != nil {
		targets, err := r.GatewaysFunc(ctx)
		if err != nil {
			log.Printf("reconcile: resolve gateway targets failed, skipping cycle: %v", err)
			return
		}
		// Prune-guard is a SUPERSET across ALL gateways: a team that owns an active
		// (non-revoked) key on ANY gateway is never pruned. Keys with a recorded
		// gateway_connection_id bucket by their issuing gateway, but legacy NULL-
		// gateway keys bucket by the department's CURRENT binding — so after a
		// default-gateway switch a team's keys can sit in a different gateway's
		// bucket than the gateway the team physically lives on. A per-gateway guard
		// would miss that and prune a team with live keys (#81). A superset only ever
		// errs toward NOT deleting — the safe default for a destructive prune.
		var allKeys []*ent.VirtualKey
		for _, t := range targets {
			allKeys = append(allKeys, t.Keys...)
		}
		allActiveKeyTeams := activeKeyTeamIDs(allKeys)
		for _, t := range targets {
			if t.Gateway == nil {
				continue
			}
			if _, err := r.reconcileKeysFor(ctx, t.Gateway, t.Keys); err != nil {
				log.Printf("reconcile keys cycle error: %v", err)
			}
			if _, err := r.reconcileTeamsFor(ctx, t.Gateway, t.Depts, allActiveKeyTeams); err != nil {
				log.Printf("reconcile teams cycle error: %v", err)
			}
		}
		return
	}
	// Legacy single-gateway path: an injected r.Gateway reconciled against all rows
	// (tests / a not-yet-migrated install with no GatewayConnection rows).
	if r.Gateway == nil {
		return
	}
	if _, err := r.ReconcileKeys(ctx); err != nil {
		log.Printf("reconcile keys cycle error: %v", err)
	}
	if _, err := r.ReconcileTeams(ctx); err != nil {
		log.Printf("reconcile teams cycle error: %v", err)
	}
}
