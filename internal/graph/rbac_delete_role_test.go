package graph

import (
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #36 coverage: DeleteCustomRole carries a tenant 404 oracle (a tenant-admin may
// delete only their own tenant's roles) + a permCache clear. Both were untested.

func TestDeleteCustomRole_OwnTenantSucceeds(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	ctxA := tenantAdminCtx("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "1a1a1a1a-1a1a-1a1a-1a1a-1a1a1a1a1a1a")

	role, err := mr.CreateCustomRole(ctxA, model.CreateCustomRoleInput{Name: "tenant-a-role"})
	if err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	ok, err := mr.DeleteCustomRole(ctxA, role.ID)
	if err != nil || !ok {
		t.Fatalf("own-tenant delete should succeed: ok=%v err=%v", ok, err)
	}
	if _, err := r.Ent.Role.Get(ctxA, uuid.MustParse(role.ID)); err == nil {
		t.Error("role should be deleted")
	}
}

func TestDeleteCustomRole_CrossTenantNotFound(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	ctxA := tenantAdminCtx("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "1a1a1a1a-1a1a-1a1a-1a1a-1a1a1a1a1a1a")
	ctxB := tenantAdminCtx("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "2b2b2b2b-2b2b-2b2b-2b2b-2b2b2b2b2b2b")

	role, err := mr.CreateCustomRole(ctxA, model.CreateCustomRoleInput{Name: "tenant-a-role"})
	if err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	// Tenant B tries to delete tenant A's role — reads as missing, role survives.
	if _, err := mr.DeleteCustomRole(ctxB, role.ID); err == nil {
		t.Fatal("tenant B must not delete tenant A's role")
	}
	if _, err := r.Ent.Role.Get(ctxA, uuid.MustParse(role.ID)); err != nil {
		t.Errorf("role must survive a cross-tenant delete attempt: %v", err)
	}
}

func TestDeleteCustomRole_InvalidID(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	if _, err := mr.DeleteCustomRole(adminCtx(), "not-a-uuid"); err == nil {
		t.Fatal("invalid id must error")
	}
}
