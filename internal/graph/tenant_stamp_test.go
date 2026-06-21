package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// A caller that belongs to a tenant must have their created resources stamped
// with that tenant (LLD-10 §1.5 STAMP); a platform admin (no tenant) leaves the
// row untenanted. This makes the data layer forward-correct ahead of the read
// sweep (no NULL-tenant orphans for tenant-scoped users).
func TestCreateAgent_StampsCallerTenant(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	seedActiveTemplate(t, r, "goose")

	tid := uuid.New()
	// a regular user that belongs to a tenant
	owner, _ := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "tu", Email: "tu@x.io", Password: "TenantPass12", Role: model.RoleUser,
	})
	tctx := tenantUserCtx(owner.ID, tid.String())

	ag, err := mr.CreateAgent(tctx, model.CreateAgentInput{Name: "a", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	row := r.Ent.Agent.GetX(ctx, uuid.MustParse(ag.ID))
	if row.TenantID == nil || *row.TenantID != tid {
		t.Fatalf("agent tenant not stamped: got %v want %s", row.TenantID, tid)
	}

	// a platform admin (no tenant) → untenanted
	ag2, err := mr.CreateAgent(adminCtx(), model.CreateAgentInput{Name: "b", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent(admin): %v", err)
	}
	if r.Ent.Agent.GetX(ctx, uuid.MustParse(ag2.ID)).TenantID != nil {
		t.Fatal("admin-created agent should be untenanted")
	}
}

func TestUpsertArtifact_StampsCallerTenant(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	tid := uuid.New()
	tctx := tenantUserCtx(uuid.NewString(), tid.String())
	a, err := mr.UpsertArtifact(tctx, model.UpsertArtifactInput{
		Name: "cfg", Kind: model.ArtifactKindConfig, Version: "1", URI: "u",
	})
	if err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	row := r.Ent.Artifact.GetX(ctx, uuid.MustParse(a.ID))
	if row.TenantID == nil || *row.TenantID != tid {
		t.Fatalf("artifact tenant not stamped: %v", row.TenantID)
	}
}

// tenantUserCtx is a regular user that belongs to a tenant.
func tenantUserCtx(userID, tenantID string) context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: userID, Role: auth.RoleUser, TenantID: tenantID})
}
