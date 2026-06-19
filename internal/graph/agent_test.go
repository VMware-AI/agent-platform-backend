package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestUpsertAgentTemplate(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	cmd := `su - {{AGENT_USER}} -c "curl -fsSL https://qoder.com/install | bash"`
	tpl, err := mr.UpsertAgentTemplate(ctx, model.UpsertAgentTemplateInput{
		Kind: "qoder", Display: "Qoder",
		InstallMethod: model.InstallMethodCurl, InstallCommand: &cmd,
		Status: model.AgentTemplateStatusActive,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if tpl.Kind != "qoder" || tpl.InstallMethod != model.InstallMethodCurl {
		t.Fatalf("unexpected template: %+v", tpl)
	}

	// upsert again with same kind => update, not duplicate
	if _, err := mr.UpsertAgentTemplate(ctx, model.UpsertAgentTemplateInput{
		Kind: "qoder", Display: "Qoder v2",
		InstallMethod: model.InstallMethodCurl, Status: model.AgentTemplateStatusActive,
	}); err != nil {
		t.Fatalf("update template: %v", err)
	}
	list, _ := qr.AgentTemplates(ctx)
	if len(list) != 1 {
		t.Fatalf("expected 1 template after upsert, got %d", len(list))
	}
	if list[0].Display != "Qoder v2" {
		t.Fatalf("template not updated: %+v", list[0])
	}
}

func TestCreateAgent_OwnerScoping(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	alice := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "11111111-1111-1111-1111-111111111111", Role: auth.RoleUser})
	bob := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "22222222-2222-2222-2222-222222222222", Role: auth.RoleUser})
	admin := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "33333333-3333-3333-3333-333333333333", Role: auth.RoleAdmin})

	if _, err := mr.CreateAgent(alice, model.CreateAgentInput{Name: "a1", AgentType: "goose"}); err != nil {
		t.Fatalf("alice create: %v", err)
	}
	if _, err := mr.CreateAgent(bob, model.CreateAgentInput{Name: "b1", AgentType: "xiaoguai"}); err != nil {
		t.Fatalf("bob create: %v", err)
	}

	// alice sees only her own
	aList, _ := qr.Agents(alice)
	if len(aList) != 1 || aList[0].Name != "a1" {
		t.Fatalf("alice should see only her agent: %+v", aList)
	}
	// admin sees all
	adminList, _ := qr.Agents(admin)
	if len(adminList) != 2 {
		t.Fatalf("admin should see all agents, got %d", len(adminList))
	}
}

func TestSetAgentStatus_NotOwnerForbidden(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	alice := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "11111111-1111-1111-1111-111111111111", Role: auth.RoleUser})
	bob := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "22222222-2222-2222-2222-222222222222", Role: auth.RoleUser})

	ag, err := mr.CreateAgent(alice, model.CreateAgentInput{Name: "a1", AgentType: "goose"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ag.Status != model.AgentStatusProvisioning {
		t.Fatalf("default status = %v", ag.Status)
	}

	// bob cannot change alice's agent
	if _, err := mr.SetAgentStatus(bob, ag.ID, model.AgentStatusStopped); err == nil {
		t.Fatal("bob should be forbidden from changing alice's agent")
	}
	// alice can
	updated, err := mr.SetAgentStatus(alice, ag.ID, model.AgentStatusRunning)
	if err != nil {
		t.Fatalf("alice set status: %v", err)
	}
	if updated.Status != model.AgentStatusRunning {
		t.Fatalf("status = %v, want running", updated.Status)
	}
}
