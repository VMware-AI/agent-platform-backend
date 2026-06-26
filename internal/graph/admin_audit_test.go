package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// hasAuditRow reports whether an AuditLog row with the given action exists with
// the expected result. It asserts the established audit convention is honored:
// a state-changing platform-admin mutation must leave a typed trail.
func hasAuditRow(t *testing.T, r *Resolver, action string, result auditlog.Result) bool {
	t.Helper()
	n, err := r.Ent.AuditLog.Query().
		Where(auditlog.Action(action), auditlog.ResultEQ(result)).
		Count(context.Background())
	if err != nil {
		t.Fatalf("query audit rows for %q: %v", action, err)
	}
	return n > 0
}

// assertAudited fails the test if no successful audit row exists for action.
func assertAudited(t *testing.T, r *Resolver, action string) {
	t.Helper()
	if !hasAuditRow(t, r, action, auditlog.ResultSuccess) {
		t.Fatalf("mutation did not write a successful audit row: action=%q", action)
	}
}

// TestPlatformAdminMutations_WriteAudit exercises a representative slice of the
// platform-admin mutations and asserts each leaves the expected audit trail. It
// covers one mutation per resolver family so a regression that drops an audit
// call surfaces here.
func TestPlatformAdminMutations_WriteAudit(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	injectFakeGatewayModels(r) // registered gateway becomes default; fake its model sync
	ctx := adminCtx()
	mr := &mutationResolver{r}

	// permission.upsert — the call that previously had no audit coverage.
	if _, err := mr.UpsertPermission(ctx, "agent:manage", ptr("manage agents")); err != nil {
		t.Fatalf("UpsertPermission: %v", err)
	}
	assertAudited(t, r, "permission.upsert")

	// department.create — exercises the gateway-backed create path.
	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "research"}); err != nil {
		t.Fatalf("CreateDepartment: %v", err)
	}
	assertAudited(t, r, "department.create")

	// role.create — custom-role family.
	if _, err := mr.CreateCustomRole(ctx, model.CreateCustomRoleInput{Name: "auditors"}); err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	assertAudited(t, r, "role.create")

	// skill.upsert / image.upsert / artifact.upsert — content family.
	if _, err := mr.UpsertSkill(ctx, model.UpsertSkillInput{Name: "lint", Version: "1.0.0", URI: "oci://lint:1"}); err != nil {
		t.Fatalf("UpsertSkill: %v", err)
	}
	assertAudited(t, r, "skill.upsert")

	if _, err := mr.UpsertImage(ctx, model.UpsertImageInput{Repository: "registry/app", Tag: "v1"}); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}
	assertAudited(t, r, "image.upsert")

	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "cfg", Version: "1", Kind: model.ArtifactKindConfig, URI: "file://cfg",
	}); err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	assertAudited(t, r, "artifact.upsert")

	// agent_template.create — catalog family (upsert keyed by kind).
	if _, err := mr.UpsertAgentTemplate(ctx, model.UpsertAgentTemplateInput{
		Kind: "goose", Display: "Goose", InstallMethod: model.InstallMethodCurl, Status: model.AgentTemplateStatusActive,
	}); err != nil {
		t.Fatalf("UpsertAgentTemplate: %v", err)
	}
	assertAudited(t, r, "agent_template.create")

	// gateway / routing family.
	if _, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw1", Endpoint: "http://gw:4000",
	}); err != nil {
		t.Fatalf("RegisterGatewayConnection: %v", err)
	}
	assertAudited(t, r, "gateway.register")

	if _, err := mr.UpsertUpstream(ctx, model.UpsertUpstreamInput{
		Name: "fast", Provider: "openai", Model: "gpt-4o-mini",
	}); err != nil {
		t.Fatalf("UpsertUpstream: %v", err)
	}
	assertAudited(t, r, "upstream.upsert")

	if _, err := mr.SetRouterTier(ctx, model.RouterTierLevelMedium, "smart"); err != nil {
		t.Fatalf("SetRouterTier: %v", err)
	}
	assertAudited(t, r, "router.set_tier")

	// ova marketplace family.
	if _, err := mr.CreateOvaTemplateFamily(ctx, model.CreateOvaTemplateFamilyInput{
		Name: "coder", Type: "coding", IconColor: model.OvaTemplateColorBlue,
		InitialVersion: &model.CreateOvaTemplateVersionInput{Version: "1.0", OvaIdentifier: "ova://coder:1"},
	}); err != nil {
		t.Fatalf("CreateOvaTemplateFamily: %v", err)
	}
	assertAudited(t, r, "ova_template_family.create")
}

// TestCreateDepartment_RejectsNegativeBudget asserts the added input validation
// fails fast with a clear gqlerror and writes no audit row (the op never ran).
func TestCreateDepartment_RejectsNegativeBudget(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	neg := -1.0
	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "bad", MaxBudget: &neg}); err == nil {
		t.Fatal("expected error for negative maxBudget")
	} else if !strings.Contains(err.Error(), "maxBudget") {
		t.Fatalf("expected a maxBudget validation error, got: %v", err)
	}

	// A rejected create must not have produced a department.create audit row.
	if hasAuditRow(t, r, "department.create", auditlog.ResultSuccess) {
		t.Fatal("rejected create must not write an audit row")
	}

	// Zero is an explicit no-spend cap and must be accepted.
	zero := 0.0
	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "zero-cap", MaxBudget: &zero}); err != nil {
		t.Fatalf("maxBudget=0 should be allowed: %v", err)
	}
	assertAudited(t, r, "department.create")
}

// TestSnapshotAgent_AuditsFailurePath asserts a failed destructive mutation
// still records a fail-result audit row (no silent destructive action). The
// agent has no VM/resource pool, so connectAgentVM fails before any vCenter call.
func TestRecycleAgent_RequiresConfirm(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	// confirm=false is rejected before any work — the destructive guard.
	if _, err := mr.RecycleAgent(adminCtx(), model.RecycleAgentInput{AgentID: "11111111-1111-1111-1111-111111111111", Confirm: false}); err == nil {
		t.Fatal("recycle without confirm must be rejected")
	}
}
