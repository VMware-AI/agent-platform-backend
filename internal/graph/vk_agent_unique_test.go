package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
)

// TestVirtualKey_AgentUniqueIndex pins the 1:1 agent↔key invariant at the DB layer
// (#32): the partial unique index (agent_id WHERE status <> 'revoked') makes a
// second active key for the same agent fail deterministically, closing the
// read-then-create race in IssueVirtualKey. A revoked key frees the agent; a
// NULL agent_id (user-level key) is unconstrained.
func TestVirtualKey_AgentUniqueIndex(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	aid, uid := uuid.New(), uuid.New()

	r.Ent.VirtualKey.Create().SetLitellmKey("k1").SetUserID(uid).SetAgentID(aid).SaveX(ctx)

	// a second ACTIVE key for the same agent must violate the partial unique index.
	if _, err := r.Ent.VirtualKey.Create().SetLitellmKey("k2").SetUserID(uid).SetAgentID(aid).Save(ctx); err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("second active key for the same agent must hit the unique index, got %v", err)
	}

	// revoking the first frees the agent — re-issuing is allowed (partial WHERE).
	first := r.Ent.VirtualKey.Query().Where(virtualkey.AgentID(aid)).FirstX(ctx)
	r.Ent.VirtualKey.UpdateOne(first).SetStatus(virtualkey.StatusRevoked).SaveX(ctx)
	if _, err := r.Ent.VirtualKey.Create().SetLitellmKey("k3").SetUserID(uid).SetAgentID(aid).Save(ctx); err != nil {
		t.Fatalf("after revoke, a new key for the agent must be allowed: %v", err)
	}

	// NULL agent_id (user-level keys) is unconstrained: many are allowed.
	r.Ent.VirtualKey.Create().SetLitellmKey("u1").SetUserID(uid).SaveX(ctx)
	if _, err := r.Ent.VirtualKey.Create().SetLitellmKey("u2").SetUserID(uid).Save(ctx); err != nil {
		t.Fatalf("user-level keys (NULL agent_id) must not be constrained: %v", err)
	}
}
