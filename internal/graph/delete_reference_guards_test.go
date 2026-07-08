package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/membership"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #34 referential guards: a resource pool / department that other rows still
// reference (soft FKs, no DB cascade) must not be deletable out from under them.

func TestDeleteResourcePool_RefusedWhenAgentReferences(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	ref := "vault://pools/x"
	created, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "pool", Endpoint: "https://vc", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("CreateResourcePool: %v", err)
	}
	pid := uuid.MustParse(created.Pool.ID)
	if _, err := r.Ent.Agent.Create().
		SetName("a").SetAgentType("goose").SetStatus(agent.StatusStopped).
		SetOwnerUserID(uuid.New()).SetResourcePoolID(pid).Save(ctx); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	if _, err := mr.DeleteResourcePool(ctx, created.Pool.ID); err == nil {
		t.Fatal("must refuse to delete a pool an agent still references")
	}
	if n, _ := r.Ent.ResourcePool.Query().Count(ctx); n != 1 {
		t.Fatalf("pool must NOT be deleted while referenced, count=%d", n)
	}
}

func TestDeleteDepartment_RefusedWhenHasMembers(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "research"})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	did := uuid.MustParse(dept.ID)
	if _, err := r.Ent.Membership.Create().
		SetUserID(uuid.New()).SetDepartmentID(did).SetRole(membership.RoleUser).Save(ctx); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	if _, err := mr.DeleteDepartment(ctx, dept.ID); err == nil {
		t.Fatal("must refuse to delete a department that still has members")
	}
	if n, _ := r.Ent.Department.Query().Count(ctx); n != 1 {
		t.Fatalf("department must NOT be deleted while it has members, count=%d", n)
	}
}

// #64-5: deleting a department whose litellm team still has live keys would orphan
// those keys on its (possibly non-default) gateway — revoke routing falls back to
// the default gateway once the department row is gone, so the real key is never
// revoked. DeleteDepartment must refuse while a non-revoked key references the team.
func TestDeleteDepartment_RefusedWhenHasActiveKeys(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "research"})
	if err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	did := uuid.MustParse(dept.ID)
	// A live key minted under this department (organization_id = dept id; the
	// model-routing schema replaced the prior team_id with organization_id).
	vk, err := r.Ent.VirtualKey.Create().
		SetLitellmKey("sk-live").SetMaskedKey("sk-***").SetName("dept-live-key").
		SetModelGatewayID(uuid.New()).
		SetOrganizationID(did.String()).SetStatus(virtualkey.StatusActive).Save(ctx)
	if err != nil {
		t.Fatalf("seed virtual key: %v", err)
	}

	if _, err := mr.DeleteDepartment(ctx, dept.ID); err == nil {
		t.Fatal("must refuse to delete a department with active virtual keys")
	}
	if n, _ := r.Ent.Department.Query().Count(ctx); n != 1 {
		t.Fatalf("department must NOT be deleted while keys reference it, count=%d", n)
	}

	// Once the key is revoked (e.g. the agent was recycled, revoking on the correct
	// gateway while the department still resolved), deletion is allowed.
	if _, err := r.Ent.VirtualKey.UpdateOne(vk).SetStatus(virtualkey.StatusRevoked).Save(ctx); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if ok, err := mr.DeleteDepartment(ctx, dept.ID); err != nil || !ok {
		t.Fatalf("DeleteDepartment after revoke: ok=%v err=%v", ok, err)
	}
}
