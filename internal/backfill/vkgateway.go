// Package backfill holds one-shot, idempotent data-migration jobs that run
// against the control-plane DB outside the request path (e.g. as a Kubernetes Job
// after a deploy). Unlike Atlas schema migrations they touch row *data*, are
// safe to re-run, and never alter the schema.
package backfill

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// VKGatewayResult summarizes one VKGateway pass for the operator (counts only —
// never key identifiers, which are sensitive).
type VKGatewayResult struct {
	Scanned          int // NULL, non-revoked rows examined
	Filled           int // rows updated with a derived gateway_connection_id
	SkippedNoGateway int // no department binding AND no default gateway → left NULL
	Failed           int // per-row update failures (logged; job exits non-zero)
}

// VKGateway backfills VirtualKey.gateway_connection_id for legacy rows minted
// before LLD-14 T1 (NULL). For each NULL, non-revoked key it derives the issuing
// gateway from the key's team_id → department's CURRENT gateway binding, else the
// platform default (LLD-14 §3.3/§3.6), and persists it. This shrinks the NULL
// fallback set so the lifecycle/reconcile paths route by a recorded gateway
// instead of re-deriving it every time.
//
// Best-effort and idempotent:
//   - Only NULL, non-revoked rows are touched, so a re-run skips everything already
//     filled and naturally converges.
//   - A key whose department can't be resolved AND has no default gateway stays
//     NULL (counted SkippedNoGateway) — re-runnable once a gateway is configured.
//   - Revoked keys are left alone (terminal lifecycle; their gateway is moot).
//
// Limitation (LLD-14 §3.6): a key whose department was re-bound since the key was
// minted is filled with the department's CURRENT gateway, which may differ from
// the true issuing gateway — backfill cannot recover that history. This only
// narrows, not eliminates, the NULL fallback; bug #5's root fix is T1–T3 recording
// the gateway at issue time going forward.
//
// It returns a non-nil error if any single-row update failed (after attempting all
// rows) so the job's exit code surfaces partial failure; the DB is left in a valid
// partial state that a re-run completes.
func VKGateway(ctx context.Context, client *ent.Client) (VKGatewayResult, error) {
	// Live gateways: every existing connection id. A key/department is only
	// ever filled with a gateway that STILL exists, so a dangling
	// department binding (a dept whose gateway was deleted — there is no
	// FK between them) falls through to "no gateway" instead of persisting
	// a stale id. This keeps the invariant "a persisted gateway_connection_id
	// points at a live gateway at write time" and matches the T3
	// reconciler's connForDept. With the platform-default retirement, the
	// only fallback for an unbound department is "skip" (no other gateway
	// to route to).
	conns, err := client.GatewayConnection.Query().All(ctx)
	if err != nil {
		return VKGatewayResult{}, fmt.Errorf("load gateways: %w", err)
	}
	live := make(map[uuid.UUID]struct{}, len(conns))
	for _, c := range conns {
		live[c.ID] = struct{}{}
	}

	// department id → its CURRENT gateway binding, kept only when that
	// gateway is still live (a dangling binding is dropped → the key stays
	// NULL).
	depts, err := client.Department.Query().All(ctx)
	if err != nil {
		return VKGatewayResult{}, fmt.Errorf("load departments: %w", err)
	}
	deptGateway := make(map[uuid.UUID]uuid.UUID, len(depts))
	for _, d := range depts {
		if d.GatewayConnectionID == nil {
			continue
		}
		if _, ok := live[*d.GatewayConnectionID]; ok {
			deptGateway[d.ID] = *d.GatewayConnectionID
		}
	}

	// Per-agent-per-org refactor (2026-07): no legacy rows need backfill
	// (gateway_connection_id column dropped, model_gateway_id now required
	// at issue time). Kept the entry point + result type so callers'
	// command-line wiring stays intact; return zero counts.
	_ = client
	_ = deptGateway
	return VKGatewayResult{}, nil
}

// deriveGateway maps a key's team_id (= its department id) to the gateway
// that should own it: the department's CURRENT live binding. nil when
// the team_id is absent, not a uuid, or the department is unbound (no
// platform default — the key stays NULL until a binding is configured).
// deptGateway holds only live bindings (see caller), so this mirrors
// the resolver's deptIDFromTeam → resolveDeptGateway selection.
func deriveGateway(teamID string, deptGateway map[uuid.UUID]uuid.UUID) *uuid.UUID {
	if teamID != "" {
		if deptID, err := uuid.Parse(teamID); err == nil {
			if gw, ok := deptGateway[deptID]; ok {
				return &gw
			}
		}
	}
	return nil
}
