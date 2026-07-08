package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/membership"
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
