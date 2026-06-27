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

// Reconciler compares gateway keys/teams against governance rows.
type Reconciler struct {
	Ent     *ent.Client
	Gateway Gateway
	// GatewayFunc, when set, resolves the gateway to reconcile at the start of each
	// Run cycle (the platform default GatewayConnection, LLD-13 §3.3) — there is no
	// process-wide gateway anymore. A nil result skips that cycle. (Reconciling each
	// of several gateways is OQ-5, a follow-up.)
	GatewayFunc func(context.Context) Gateway
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

// ReconcileKeys runs one pass. It returns an error only when it cannot read both
// sides; per-item heal failures are logged and counted, not fatal.
func (r *Reconciler) ReconcileKeys(ctx context.Context) (*Report, error) {
	gwKeys, err := r.Gateway.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway keys: %w", err)
	}
	rows, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list governance rows: %w", err)
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
		r.heal(ctx, rep)
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

// ReconcileTeams compares gateway teams against department rows. Mirrors
// ReconcileKeys. Gateway team orphans (no backing department) are pruned under
// Prune; dangling department references are only reported — re-creating vs
// clearing the link is a policy decision left to an operator.
func (r *Reconciler) ReconcileTeams(ctx context.Context) (*TeamReport, error) {
	gwTeams, err := r.Gateway.ListTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway teams: %w", err)
	}
	depts, err := r.Ent.Department.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
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
				if err := r.Gateway.DeleteTeam(ctx, teamID); err != nil {
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
func (r *Reconciler) heal(ctx context.Context, rep *Report) {
	if allUnmatched(len(rep.GatewayOrphans), rep.GatewayKeys, rep.DBKeys) {
		log.Printf("reconcile: SKIP key heal — all %d gateway keys unmatched against %d DB rows; likely identifier mismatch or partial list, refusing to prune/revoke",
			rep.GatewayKeys, rep.DBKeys)
		return
	}
	for _, key := range rep.GatewayOrphans {
		if err := r.Gateway.DeleteKey(ctx, key); err != nil {
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
	// Resolve the gateway to reconcile this cycle (default GatewayConnection).
	// runCycle is called sequentially from Run's loop, so mutating r.Gateway here
	// is race-free.
	if r.GatewayFunc != nil {
		r.Gateway = r.GatewayFunc(ctx)
	}
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
