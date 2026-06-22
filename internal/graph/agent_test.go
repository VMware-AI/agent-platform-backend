package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/agenttemplate"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// seedTemplate inserts a catalog entry of the given kind/status for tests that
// create agents (CreateAgent requires an active template — LLD-05 §5).
func seedTemplate(t *testing.T, r *Resolver, kind string, status agenttemplate.Status) {
	t.Helper()
	_, err := r.Ent.AgentTemplate.Create().
		SetKind(kind).SetDisplay(kind).SetStatus(status).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed template %s: %v", kind, err)
	}
}

// seedActiveTemplate is the common case: an active catalog kind. Usable from other
// test files in this package without importing the agenttemplate enum.
func seedActiveTemplate(t *testing.T, r *Resolver, kind string) {
	t.Helper()
	seedTemplate(t, r, kind, agenttemplate.StatusActive)
}

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

// AgentTemplates resolves {{PLACEHOLDER}} tokens in install_command against the
// resolver's InstallVars before returning them (LLD-05 §1).
func TestAgentTemplates_ResolvesInstallPlaceholders(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.InstallVars = map[string]string{"AGENT_PKG_BASE_URL": "http://mirror/agents", "AGENT_USER": "agent"}
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	cmd := "curl {{AGENT_PKG_BASE_URL}}/goose.tar.gz && su {{AGENT_USER}} -c run"
	if _, err := mr.UpsertAgentTemplate(ctx, model.UpsertAgentTemplateInput{
		Kind: "goose", Display: "Goose",
		InstallMethod: model.InstallMethodOfflineTar, InstallCommand: &cmd,
		Status: model.AgentTemplateStatusActive,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, _ := qr.AgentTemplates(ctx)
	if len(list) != 1 || list[0].InstallCommand == nil {
		t.Fatalf("unexpected list: %+v", list)
	}
	want := "curl http://mirror/agents/goose.tar.gz && su agent -c run"
	if *list[0].InstallCommand != want {
		t.Errorf("install_command not resolved:\n got  %q\n want %q", *list[0].InstallCommand, want)
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

	seedTemplate(t, r, "goose", agenttemplate.StatusActive)
	seedTemplate(t, r, "xiaoguai", agenttemplate.StatusActive)

	if _, err := mr.CreateAgent(alice, model.CreateAgentInput{Name: "a1", AgentType: "goose"}); err != nil {
		t.Fatalf("alice create: %v", err)
	}
	if _, err := mr.CreateAgent(bob, model.CreateAgentInput{Name: "b1", AgentType: "xiaoguai"}); err != nil {
		t.Fatalf("bob create: %v", err)
	}

	// alice sees only her own
	aList, _ := qr.Agents(alice, nil, nil, nil)
	if aList.TotalCount != 1 || len(aList.Nodes) != 1 || aList.Nodes[0].Name != "a1" {
		t.Fatalf("alice should see only her agent: %+v", aList)
	}
	// admin sees all
	adminList, _ := qr.Agents(admin, nil, nil, nil)
	if adminList.TotalCount != 2 || len(adminList.Nodes) != 2 {
		t.Fatalf("admin should see all agents, got %d", adminList.TotalCount)
	}
}

// CreateAgent must reject types that are unknown or deferred (LLD-05 §5).
func TestCreateAgent_RejectsInactiveType(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	user := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "11111111-1111-1111-1111-111111111111", Role: auth.RoleUser})

	// unknown type → rejected
	if _, err := mr.CreateAgent(user, model.CreateAgentInput{Name: "x", AgentType: "nope"}); err == nil {
		t.Fatal("unknown agent type must be rejected")
	}

	// deferred type → rejected
	seedTemplate(t, r, "hermes", agenttemplate.StatusDeferred)
	if _, err := mr.CreateAgent(user, model.CreateAgentInput{Name: "h", AgentType: "hermes"}); err == nil {
		t.Fatal("deferred agent type must be rejected")
	}

	// active type → allowed
	seedTemplate(t, r, "goose", agenttemplate.StatusActive)
	if _, err := mr.CreateAgent(user, model.CreateAgentInput{Name: "g", AgentType: "goose"}); err != nil {
		t.Fatalf("active agent type should be allowed: %v", err)
	}
}

// No existence oracle: a non-owner probing a REAL agent they don't own and a
// NONEXISTENT agent must get byte-identical errors, so the response can't reveal
// which ids exist (task_1878902f).
func TestSetAgentStatus_NoExistenceOracle(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	seedActiveTemplate(t, r, "goose")

	alice := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "11111111-1111-1111-1111-111111111111", Role: auth.RoleUser})
	bob := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "22222222-2222-2222-2222-222222222222", Role: auth.RoleUser})

	ag, err := mr.CreateAgent(alice, model.CreateAgentInput{Name: "a", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	_, errExisting := mr.SetAgentStatus(bob, ag.ID, model.AgentStatusStopped)                                 // exists, not bob's
	_, errMissing := mr.SetAgentStatus(bob, "99999999-9999-9999-9999-999999999999", model.AgentStatusStopped) // does not exist
	if errExisting == nil || errMissing == nil {
		t.Fatalf("both must error: existing=%v missing=%v", errExisting, errMissing)
	}
	if errExisting.Error() != errMissing.Error() {
		t.Fatalf("existence oracle: not-yours=%q vs missing=%q must be identical", errExisting, errMissing)
	}
	// and it must NOT be the old revealing "forbidden: not your agent"
	if got := errExisting.Error(); !strings.Contains(got, "not found") {
		t.Errorf("owner-scoped denial should read as not-found, got %q", got)
	}
}

func TestSetAgentStatus_NotOwnerForbidden(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	alice := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "11111111-1111-1111-1111-111111111111", Role: auth.RoleUser})
	bob := auth.WithCurrentUser(ctx, &auth.CurrentUser{ID: "22222222-2222-2222-2222-222222222222", Role: auth.RoleUser})

	seedTemplate(t, r, "goose", agenttemplate.StatusActive)

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
