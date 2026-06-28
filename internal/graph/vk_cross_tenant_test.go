package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestCrossTenant_VirtualKeyByIDGuards pins the #30 fix: virtual-key by-id
// mutations (issue / revoke / regenerate / setEnabled) enforce an owner-tenant
// 404 oracle, so a tenant-admin cannot mint/rotate/disable another tenant's
// billable key. Platform-admin and no-auth (resolver-level) callers are
// unaffected (verified by the existing suite still passing).
func TestCrossTenant_VirtualKeyByIDGuards(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	mr := &mutationResolver{r}
	setupCtx := context.Background() // no-auth: bypasses the guard for setup only

	tA, tB := uuid.New(), uuid.New()
	userB := r.Ent.User.Create().
		SetUsername("ub-vk").SetEmail("ubvk@x.io").SetPasswordHash("h").
		SetTenantID(tB).SaveX(setupCtx)

	issued, err := mr.IssueVirtualKey(setupCtx, model.IssueVirtualKeyInput{UserID: userB.ID.String()})
	if err != nil {
		t.Fatalf("issue (setup): %v", err)
	}
	kid := issued.VirtualKey.ID

	// tenant-A admin must NOT act on tenant-B's key (reads as missing).
	taCtx := tenantAdminCtx(uuid.NewString(), tA.String())
	if _, err := mr.RevokeVirtualKey(taCtx, kid); err == nil {
		t.Fatal("tenant-A admin must not revoke tenant-B's key")
	}
	if _, err := mr.RegenerateVirtualKey(taCtx, kid); err == nil {
		t.Fatal("tenant-A admin must not regenerate tenant-B's key")
	}
	if _, err := mr.SetVirtualKeyEnabled(taCtx, kid, false); err == nil {
		t.Fatal("tenant-A admin must not toggle tenant-B's key")
	}
	// ...nor mint a fresh key for tenant-B's user.
	if _, err := mr.IssueVirtualKey(taCtx, model.IssueVirtualKeyInput{UserID: userB.ID.String()}); err == nil {
		t.Fatal("tenant-A admin must not mint a key for tenant-B's user")
	}

	// Sanity: tenant-B's own admin CAN manage its key (guard isn't over-blocking).
	tbCtx := tenantAdminCtx(uuid.NewString(), tB.String())
	if _, err := mr.SetVirtualKeyEnabled(tbCtx, kid, false); err != nil {
		t.Fatalf("tenant-B admin must manage its own tenant's key: %v", err)
	}
}

// TestCrossTenant_IssueVirtualKeyReferenceGuards pins bug-audit-0628b #4 (#64):
// IssueVirtualKey must validate the OTHER caller-supplied ids (agentId /
// rateLimitPolicyId / teamId), not just userId. A tenant-A admin issuing a key for
// THEIR OWN user must not be able to reach a tenant-B agent (1:1-slot DoS),
// tenant-B's rate-limit policy (cross-tenant read/oracle), or tenant-B's
// department/team (budget misattribution). Each foreign reference reads as missing
// (404 oracle), while a legitimately-owned reference still succeeds.
func TestCrossTenant_IssueVirtualKeyReferenceGuards(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	mr := &mutationResolver{r}
	bg := context.Background() // no-auth: bypasses guards for setup only

	tA, tB := uuid.New(), uuid.New()
	// tenant-A's own admin + a tenant-A user that admin may key.
	aliceID := uuid.NewString()
	taCtx := tenantAdminCtx(aliceID, tA.String())
	userA := r.Ent.User.Create().SetUsername("ua-vkref").SetEmail("uavkref@x.io").
		SetPasswordHash("h").SetTenantID(tA).SaveX(bg)
	uaID := userA.ID.String()

	// tenant-B owns: an agent, a private rate-limit policy, and a department — all
	// referenced by id below. The agent's owner is a tenant-B user (owner track).
	userB := r.Ent.User.Create().SetUsername("ub-vkref").SetEmail("ubvkref@x.io").
		SetPasswordHash("h").SetTenantID(tB).SaveX(bg)
	agentB := r.Ent.Agent.Create().SetName("agB").SetAgentType("goose").
		SetOwnerUserID(userB.ID).SetTenantID(tB).SaveX(bg)
	agBID := agentB.ID.String()
	polB := r.Ent.RateLimitPolicy.Create().SetName("polB").SetRpm(60).
		SetTenantID(tB).SaveX(bg)
	polBID := polB.ID.String()
	deptB := r.Ent.Department.Create().SetName("deptB").SetTenantID(tB).SaveX(bg)
	deptBID := deptB.ID.String() // teamId == department id (LLD-13 §3.3)

	// (1) cross-tenant agent → 1:1-slot DoS is rejected (reads as missing).
	if _, err := mr.IssueVirtualKey(taCtx, model.IssueVirtualKeyInput{UserID: uaID, AgentID: &agBID}); err == nil {
		t.Fatal("tenant-A admin must not bind a key to tenant-B's agent")
	}
	// The foreign agent's 1:1 slot must remain FREE — tenant-B can still key it.
	tbCtx := tenantAdminCtx(uuid.NewString(), tB.String())
	if _, err := mr.IssueVirtualKey(tbCtx, model.IssueVirtualKeyInput{UserID: userB.ID.String(), AgentID: &agBID}); err != nil {
		t.Fatalf("tenant-B admin must still be able to key its own agent (slot not stolen): %v", err)
	}

	// (2) cross-tenant rate-limit policy → rejected (no read / no existence oracle).
	if _, err := mr.IssueVirtualKey(taCtx, model.IssueVirtualKeyInput{UserID: uaID, RateLimitPolicyID: &polBID}); err == nil {
		t.Fatal("tenant-A admin must not apply tenant-B's rate-limit policy")
	}

	// (3) cross-tenant department/team → rejected (no foreign-budget mint).
	if _, err := mr.IssueVirtualKey(taCtx, model.IssueVirtualKeyInput{UserID: uaID, TeamID: &deptBID}); err == nil {
		t.Fatal("tenant-A admin must not mint under tenant-B's department/team")
	}

	// Positive: tenant-A admin CAN reference its OWN tenant's resources (guards are
	// not over-blocking). Seed tenant-A versions of each reference.
	agentA := r.Ent.Agent.Create().SetName("agA").SetAgentType("goose").
		SetOwnerUserID(userA.ID).SetTenantID(tA).SaveX(bg)
	agAID := agentA.ID.String()
	polA := r.Ent.RateLimitPolicy.Create().SetName("polA").SetRpm(30).
		SetTenantID(tA).SaveX(bg)
	polAID := polA.ID.String()
	deptA := r.Ent.Department.Create().SetName("deptA").SetTenantID(tA).SaveX(bg)
	deptAID := deptA.ID.String()

	if _, err := mr.IssueVirtualKey(taCtx, model.IssueVirtualKeyInput{
		UserID: uaID, AgentID: &agAID, RateLimitPolicyID: &polAID, TeamID: &deptAID,
	}); err != nil {
		t.Fatalf("tenant-A admin must be able to key its own tenant's agent/policy/department: %v", err)
	}
}
