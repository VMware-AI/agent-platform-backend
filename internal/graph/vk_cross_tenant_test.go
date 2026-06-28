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
