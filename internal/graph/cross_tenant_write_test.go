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
