package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/artifact"
)

// TestCrossTenant_Write404Oracle verifies LLD-10 §1.5 write isolation: a
// tenant-admin can mutate only their own tenant's rows; a cross-tenant or
// platform row reads as missing (notFound, not a distinguishable forbidden, so
// there is no existence oracle). AC-2 / AC-7.
func TestCrossTenant_Write404Oracle(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()
	mr := &mutationResolver{r}

	artB := r.Ent.Artifact.Create().SetName("b").SetKind("config").SetVersion("1").SetURI("u").SetTenantID(tB).SaveX(ctx)
	artA := r.Ent.Artifact.Create().SetName("a").SetKind("config").SetVersion("1").SetURI("u").SetTenantID(tA).SaveX(ctx)
	artP := r.Ent.Artifact.Create().SetName("p").SetKind("config").SetVersion("1").SetURI("u").SaveX(ctx) // platform NULL

	taCtx := tenantAdminCtx(uuid.NewString(), tA.String())

	// cross-tenant delete → error, and the row survives
	if _, err := mr.DeleteArtifact(taCtx, artB.ID.String()); err == nil {
		t.Fatal("tenant-admin deleting another tenant's artifact must fail")
	}
	if !r.Ent.Artifact.Query().Where(artifact.ID(artB.ID)).ExistX(ctx) {
		t.Fatal("cross-tenant artifact must NOT be deleted")
	}

	// platform (NULL-tenant) row is also not deletable by a tenant-admin
	if _, err := mr.DeleteArtifact(taCtx, artP.ID.String()); err == nil {
		t.Fatal("tenant-admin must not delete a platform artifact")
	}

	// own-tenant delete → ok
	if ok, err := mr.DeleteArtifact(taCtx, artA.ID.String()); err != nil || !ok {
		t.Fatalf("tenant-admin deleting own artifact: ok=%v err=%v", ok, err)
	}

	// admin can delete the platform artifact
	if ok, err := mr.DeleteArtifact(adminCtx(), artP.ID.String()); err != nil || !ok {
		t.Fatalf("admin delete platform artifact: ok=%v err=%v", ok, err)
	}

	// a non-existent id is indistinguishable from a cross-tenant one (same error)
	if _, err := mr.DeleteArtifact(taCtx, uuid.NewString()); err == nil {
		t.Fatal("deleting a missing artifact must fail the same way")
	}
}

// TestCrossTenant_RBACMutations verifies the role/user guards on
// setRolePermissions / assignUserRole / removeUserRole (LLD-10 ①): a tenant-admin
// cannot touch another tenant's role or user.
func TestCrossTenant_RBACMutations(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()
	mr := &mutationResolver{r}

	roleB := r.Ent.Role.Create().SetName("rb").SetTenantID(tB).SaveX(ctx)
	roleA := r.Ent.Role.Create().SetName("ra").SetTenantID(tA).SaveX(ctx)
	userB := r.Ent.User.Create().SetUsername("ub2").SetEmail("ub2@x.io").SetPasswordHash("h").SetTenantID(tB).SaveX(ctx)
	userA := r.Ent.User.Create().SetUsername("ua2").SetEmail("ua2@x.io").SetPasswordHash("h").SetTenantID(tA).SaveX(ctx)

	taCtx := tenantAdminCtx(uuid.NewString(), tA.String())

	if _, err := mr.SetRolePermissions(taCtx, roleB.ID.String(), []string{"audit:view"}); err == nil {
		t.Fatal("setRolePermissions on cross-tenant role must fail")
	}
	if _, err := mr.SetRolePermissions(taCtx, roleA.ID.String(), []string{"audit:view"}); err != nil {
		t.Fatalf("setRolePermissions own role: %v", err)
	}
	if _, err := mr.AssignUserRole(taCtx, userA.ID.String(), roleB.ID.String()); err == nil {
		t.Fatal("assigning a cross-tenant role must fail")
	}
	if _, err := mr.AssignUserRole(taCtx, userB.ID.String(), roleA.ID.String()); err == nil {
		t.Fatal("assigning to a cross-tenant user must fail")
	}
	if ok, err := mr.AssignUserRole(taCtx, userA.ID.String(), roleA.ID.String()); err != nil || !ok {
		t.Fatalf("assign own role to own user: ok=%v err=%v", ok, err)
	}
	if ok, err := mr.RemoveUserRole(taCtx, userA.ID.String(), roleA.ID.String()); err != nil || !ok {
		t.Fatalf("remove own role from own user: ok=%v err=%v", ok, err)
	}
}
