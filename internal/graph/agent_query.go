package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/predicate"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/google/uuid"
)

// agentVisibilityPredicates builds the SECURITY-SENSITIVE visibility/scope
// predicates for the Agents list (LLD-10 §1.3 + §2.3), returning the exact set
// the inline resolver applied via successive q.Where(...) calls (ent ANDs them
// identically whether added one-by-one or in a single call):
//
//   - Three-track visibility: admin → all (no predicate); tenant-admin → their
//     whole tenant (or fail-closed deny-all on a malformed tenant); regular user
//     → only their own agents (owner track), failing CLOSED — a session id that
//     isn't a valid UUID scopes to zero rows, never an unscoped query.
//   - Soft env filter, applied AFTER the tenant/owner track (LLD-10 §2.3):
//     env_id == env OR env_id IS NULL.
func (r *Resolver) agentVisibilityPredicates(ctx context.Context, cu *auth.CurrentUser) []predicate.Agent {
	var preds []predicate.Agent
	// Three-track visibility (LLD-10 §1.3): admin → all; tenant-admin → their
	// whole tenant; regular user → only their own agents (owner track).
	switch {
	case cu.Role == auth.RoleAdmin:
		// no filter
	case cu.Role == auth.RoleTenantAdmin:
		if d := tenantScopeFor(ctx); d.apply {
			if d.denyAll {
				preds = append(preds, agent.IDEQ(uuid.Nil))
			} else {
				preds = append(preds, agent.TenantID(d.tenant))
			}
		}
	default:
		// Regular user: only their own agents (owner track). Fail CLOSED — a
		// session id that isn't a valid UUID must scope to zero rows, never fall
		// through to an unscoped (all-agents) query.
		if uid, err := uuid.Parse(cu.ID); err == nil {
			preds = append(preds, agent.OwnerUserID(uid))
		} else {
			preds = append(preds, agent.IDEQ(uuid.Nil))
		}
	}
	if env, ok := r.envScopeFor(ctx); ok { // soft env filter, after tenant (LLD-10 §2.3)
		preds = append(preds, agent.Or(agent.EnvironmentID(env), agent.EnvironmentIDIsNil()))
	}
	return preds
}
