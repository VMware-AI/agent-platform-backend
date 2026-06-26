package graph

// Edge-case coverage for the READ side of the platform-admin entities: empty
// state, argument-scoped filtering, not-found / bad-input handling, and (where
// the op paginates) out-of-range bounds. These guard against regressions where
// a query panics, returns nil instead of an empty list, leaks rows past a
// filter, or mishandles a malformed id. Seeding reuses the existing mutation
// resolvers / seed helpers from this package — no new public seed helpers are
// added (private names are suffixed _ec to avoid collisions).

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// ---------------------------------------------------------------------------
// Departments
// ---------------------------------------------------------------------------

// Empty state: no seeded departments → an empty (non-nil) slice, never a panic.
func TestDepartments_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.Departments(adminCtx())
	if err != nil {
		t.Fatalf("Departments empty: %v", err)
	}
	if got == nil {
		t.Fatal("Departments must return an empty slice, not nil")
	}
	if len(got) != 0 {
		t.Fatalf("Departments empty want 0, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// DepartmentMembers — filtering by departmentId + bad/not-found id
// ---------------------------------------------------------------------------

// Filtering: departmentMembers(departmentId:) returns only that department's
// members and never leaks a sibling department's members.
func TestDepartmentMembers_ScopedToDepartment_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Gateway = &fakeGateway{}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	d1, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "d1-ec"})
	if err != nil {
		t.Fatalf("create d1: %v", err)
	}
	d2, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "d2-ec"})
	if err != nil {
		t.Fatalf("create d2: %v", err)
	}

	// A department with no members reads back as an empty (non-nil) slice.
	if mm, err := qr.DepartmentMembers(ctx, d1.ID); err != nil {
		t.Fatalf("empty members: %v", err)
	} else if mm == nil || len(mm) != 0 {
		t.Fatalf("empty department want [] got %v", mm)
	}

	// Seed one member in d1 and a different member in d2.
	alice := "11111111-1111-1111-1111-111111111111"
	bob := "22222222-2222-2222-2222-222222222222"
	if _, err := mr.AddMembership(ctx, alice, d1.ID, nil); err != nil {
		t.Fatalf("add alice→d1: %v", err)
	}
	if _, err := mr.AddMembership(ctx, bob, d2.ID, nil); err != nil {
		t.Fatalf("add bob→d2: %v", err)
	}

	d1m, err := qr.DepartmentMembers(ctx, d1.ID)
	if err != nil {
		t.Fatalf("d1 members: %v", err)
	}
	if len(d1m) != 1 || d1m[0].UserID != alice {
		t.Fatalf("d1 must contain only alice, got %+v", d1m)
	}
}

// Bad input: an unparseable departmentId returns a clean error (no panic).
func TestDepartmentMembers_BadID_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	if _, err := qr.DepartmentMembers(adminCtx(), "not-a-uuid"); err == nil {
		t.Fatal("unparseable departmentId must return an error")
	}
}

// Not-found: a well-formed but non-existent departmentId returns a clean empty
// list for a platform admin (canManageDepartment grants admin everything; the
// query simply matches no rows) — not a panic.
func TestDepartmentMembers_NotFoundID_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	missing := "99999999-9999-9999-9999-999999999999"
	got, err := qr.DepartmentMembers(adminCtx(), missing)
	if err != nil {
		t.Fatalf("non-existent departmentId (admin): unexpected error %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("non-existent department want [] got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

func TestSkills_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.Skills(adminCtx())
	if err != nil {
		t.Fatalf("Skills empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Skills empty want [] got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

func TestImages_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.Images(adminCtx())
	if err != nil {
		t.Fatalf("Images empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Images empty want [] got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Artifacts — empty state + kind filtering
// ---------------------------------------------------------------------------

func TestArtifacts_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.Artifacts(adminCtx(), nil)
	if err != nil {
		t.Fatalf("Artifacts empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Artifacts empty want [] got %v", got)
	}
}

// Filtering: artifacts(kind:) returns only that kind; a kind with no rows yields
// an empty (non-nil) list rather than leaking other kinds.
func TestArtifacts_KindFilter_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "kpack", Kind: model.ArtifactKindKnowledge, Version: "1.0.0", URI: "k1",
	}); err != nil {
		t.Fatalf("upsert knowledge: %v", err)
	}
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "cfg", Kind: model.ArtifactKindConfig, Version: "1.0.0", URI: "c1",
	}); err != nil {
		t.Fatalf("upsert config: %v", err)
	}

	knowledge := model.ArtifactKindKnowledge
	got, err := qr.Artifacts(ctx, &knowledge)
	if err != nil {
		t.Fatalf("Artifacts(knowledge): %v", err)
	}
	if len(got) != 1 || got[0].Kind != model.ArtifactKindKnowledge {
		t.Fatalf("kind filter leaked: %+v", got)
	}

	// A kind with no matching rows → empty, not the unfiltered set.
	pkg := model.ArtifactKindPackage
	none, err := qr.Artifacts(ctx, &pkg)
	if err != nil {
		t.Fatalf("Artifacts(package): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("kind=package want [] got %+v", none)
	}
}

// ---------------------------------------------------------------------------
// ArtifactVersions — name scoping + not-found name
// ---------------------------------------------------------------------------

// Filtering: artifactVersions(name:) returns every version of that name and
// nothing from a differently-named artifact.
func TestArtifactVersions_NameScope_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	for _, v := range []string{"1.0.0", "1.1.0"} {
		if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
			Name: "goose", Kind: model.ArtifactKindPackage, Version: v, URI: "u-" + v,
		}); err != nil {
			t.Fatalf("upsert goose %s: %v", v, err)
		}
	}
	if _, err := mr.UpsertArtifact(ctx, model.UpsertArtifactInput{
		Name: "other", Kind: model.ArtifactKindPackage, Version: "1.0.0", URI: "uo",
	}); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	got, err := qr.ArtifactVersions(ctx, "goose")
	if err != nil {
		t.Fatalf("ArtifactVersions(goose): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 goose versions, got %d", len(got))
	}
	for _, v := range got {
		if v.Name != "goose" {
			t.Fatalf("ArtifactVersions leaked %q", v.Name)
		}
	}
}

// Not-found: artifactVersions for a name that doesn't exist → empty (non-nil),
// not an error.
func TestArtifactVersions_NotFoundName_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.ArtifactVersions(adminCtx(), "does-not-exist")
	if err != nil {
		t.Fatalf("ArtifactVersions(missing): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("unknown name want [] got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// AgentTemplates
// ---------------------------------------------------------------------------

func TestAgentTemplates_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.AgentTemplates(adminCtx())
	if err != nil {
		t.Fatalf("AgentTemplates empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("AgentTemplates empty want [] got %v", got)
	}
}

// A seeded catalog entry shows up in the list (sanity that empty != broken).
func TestAgentTemplates_ListsSeeded_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	seedActiveTemplate(t, r, "goose-ec")
	got, err := qr.AgentTemplates(adminCtx())
	if err != nil {
		t.Fatalf("AgentTemplates: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "goose-ec" {
		t.Fatalf("seeded template not listed: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// GatewayConnections
// ---------------------------------------------------------------------------

func TestGatewayConnections_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.GatewayConnections(adminCtx())
	if err != nil {
		t.Fatalf("GatewayConnections empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("GatewayConnections empty want [] got %v", got)
	}
}

func TestGatewayConnections_ListsSeeded_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.RegisterGatewayConnection(adminCtx(), model.RegisterGatewayConnectionInput{
		Name: "gw-ec", Endpoint: "https://litellm.internal",
	}); err != nil {
		t.Fatalf("RegisterGatewayConnection: %v", err)
	}
	got, err := qr.GatewayConnections(adminCtx())
	if err != nil {
		t.Fatalf("GatewayConnections: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gw-ec" {
		t.Fatalf("seeded gateway not listed: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Upstreams
// ---------------------------------------------------------------------------

func TestUpstreams_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.Upstreams(adminCtx())
	if err != nil {
		t.Fatalf("Upstreams empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Upstreams empty want [] got %v", got)
	}
}

func TestUpstreams_ListsSeeded_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayModels = &fakeModelManager{} // UpsertUpstream syncs a model on save
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.UpsertUpstream(adminCtx(), model.UpsertUpstreamInput{
		Name: "tier-fast-ec", Provider: model.UpstreamProviderVllm, Model: "openai/qwen-7b",
	}); err != nil {
		t.Fatalf("UpsertUpstream: %v", err)
	}
	got, err := qr.Upstreams(adminCtx())
	if err != nil {
		t.Fatalf("Upstreams: %v", err)
	}
	if len(got) != 1 || got[0].Name != "tier-fast-ec" {
		t.Fatalf("seeded upstream not listed: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// RouterTiers
// ---------------------------------------------------------------------------

func TestRouterTiers_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.RouterTiers(adminCtx())
	if err != nil {
		t.Fatalf("RouterTiers empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("RouterTiers empty want [] got %v", got)
	}
}

func TestRouterTiers_ListsSeeded_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayModels = &fakeModelManager{} // SetRouterTier syncs the complexity router
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.SetRouterTier(adminCtx(), model.RouterTierLevelSimple, "tier-fast"); err != nil {
		t.Fatalf("SetRouterTier: %v", err)
	}
	got, err := qr.RouterTiers(adminCtx())
	if err != nil {
		t.Fatalf("RouterTiers: %v", err)
	}
	if len(got) != 1 || got[0].Tier != model.RouterTierLevelSimple {
		t.Fatalf("seeded tier not listed: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// CustomRoles
// ---------------------------------------------------------------------------

// Empty state: a fresh store auto-seeds no custom roles → empty list.
func TestCustomRoles_EmptyState_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	got, err := qr.CustomRoles(adminCtx())
	if err != nil {
		t.Fatalf("CustomRoles empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("CustomRoles empty want [] got %v", got)
	}
}

func TestCustomRoles_ListsSeeded_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	if _, err := mr.CreateCustomRole(adminCtx(), model.CreateCustomRoleInput{Name: "role-ec"}); err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	got, err := qr.CustomRoles(adminCtx())
	if err != nil {
		t.Fatalf("CustomRoles: %v", err)
	}
	if len(got) != 1 || got[0].Name != "role-ec" {
		t.Fatalf("seeded role not listed: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// AgentSnapshots — argument scoping + bad/not-found id (no vCenter needed)
// ---------------------------------------------------------------------------

// Bad input: an unparseable agentId returns a clean error (no panic).
func TestAgentSnapshots_BadID_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	if _, err := qr.AgentSnapshots(adminCtx(), "not-a-uuid"); err == nil {
		t.Fatal("unparseable agentId must return an error")
	}
}

// Not-found: a well-formed but non-existent agentId resolves to a clean error
// (the VM/agent can't be found and connected) rather than a panic.
func TestAgentSnapshots_NotFoundID_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	missing := "99999999-9999-9999-9999-999999999999"
	if _, err := qr.AgentSnapshots(adminCtx(), missing); err == nil {
		t.Fatal("non-existent agentId must return an error, not nil/panic")
	}
}

// Unauthenticated: a context with no current user is rejected cleanly (exercises
// the cu == nil early return — uses a bare context with no WithCurrentUser).
func TestAgentSnapshots_Unauthenticated_ec(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	if _, err := qr.AgentSnapshots(context.Background(), "99999999-9999-9999-9999-999999999999"); err == nil {
		t.Fatal("unauthenticated AgentSnapshots must return an error")
	}
}
