package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// These tests verify LLD-10 B2 read isolation: a caller confined to tenant A
// must not see tenant B's rows, while platform-global (NULL-tenant) rows stay
// visible for the hybrid "content" entities. Resolvers are called directly
// (directive layer bypassed) so the assertions target the Where scoping itself.

func TestCrossTenant_AgentConfigs(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	r.Ent.AgentConfig.Create().SetName("a-cfg").SetAgentType("goose").SetTenantID(tA).SaveX(ctx)
	r.Ent.AgentConfig.Create().SetName("b-cfg").SetAgentType("goose").SetTenantID(tB).SaveX(ctx)
	r.Ent.AgentConfig.Create().SetName("platform-cfg").SetAgentType("goose").SaveX(ctx) // NULL tenant

	qr := &queryResolver{r}
	got, err := qr.AgentConfigs(tenantUserCtx(uuid.NewString(), tA.String()), nil)
	if err != nil {
		t.Fatalf("AgentConfigs: %v", err)
	}
	assertNames(t, names(got, func(c model.AgentConfig) string { return c.Name }),
		"a-cfg", "platform-cfg")
}

func TestCrossTenant_Artifacts(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	r.Ent.Artifact.Create().SetName("a-art").SetKind("config").SetVersion("1").SetURI("u").SetTenantID(tA).SaveX(ctx)
	r.Ent.Artifact.Create().SetName("b-art").SetKind("config").SetVersion("1").SetURI("u").SetTenantID(tB).SaveX(ctx)
	r.Ent.Artifact.Create().SetName("platform-art").SetKind("config").SetVersion("1").SetURI("u").SaveX(ctx)

	qr := &queryResolver{r}
	got, err := qr.Artifacts(tenantUserCtx(uuid.NewString(), tA.String()))
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	assertNames(t, names(got, func(a model.Artifact) string { return a.Name }),
		"a-art", "platform-art")

	// platform admin sees all three
	all, _ := qr.Artifacts(adminCtx())
	if len(all) != 3 {
		t.Fatalf("admin should see all 3 artifacts, got %d", len(all))
	}
}

func TestCrossTenant_CustomRoles(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	r.Ent.Role.Create().SetName("a-role").SetTenantID(tA).SaveX(ctx)
	r.Ent.Role.Create().SetName("b-role").SetTenantID(tB).SaveX(ctx)
	r.Ent.Role.Create().SetName("system-role").SetIsSystem(true).SaveX(ctx) // NULL tenant

	qr := &queryResolver{r}
	got, err := qr.CustomRoles(tenantAdminCtx(uuid.NewString(), tA.String()))
	if err != nil {
		t.Fatalf("CustomRoles: %v", err)
	}
	assertNames(t, names(got, func(c model.CustomRole) string { return c.Name }),
		"a-role", "system-role")
}

func TestCrossTenant_RateLimitPolicies_Strict(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	r.Ent.RateLimitPolicy.Create().SetName("a-pol").SetTenantID(tA).SaveX(ctx)
	r.Ent.RateLimitPolicy.Create().SetName("b-pol").SetTenantID(tB).SaveX(ctx)
	r.Ent.RateLimitPolicy.Create().SetName("platform-pol").SaveX(ctx) // NULL tenant

	qr := &queryResolver{r}
	got, err := qr.RateLimitPolicies(tenantAdminCtx(uuid.NewString(), tA.String()))
	if err != nil {
		t.Fatalf("RateLimitPolicies: %v", err)
	}
	// strict tenant scope: only tenant A's policy (NOT platform NULL, NOT B)
	assertNames(t, names(got, func(p model.RateLimitPolicy) string { return p.Name }), "a-pol")
}

func TestCrossTenant_MisconfiguredTenant_DenyAll(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	r.Ent.Artifact.Create().SetName("x").SetKind("config").SetVersion("1").SetURI("u").SetTenantID(uuid.New()).SaveX(ctx)

	// a caller whose tenant id is malformed must see NOTHING (fail closed), never
	// the platform/untenanted rows.
	badCtx := tenantUserCtx(uuid.NewString(), "not-a-uuid")
	got, err := (&queryResolver{r}).Artifacts(badCtx)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("malformed-tenant caller should see nothing, got %d", len(got))
	}
}

// helpers
func names[T any](xs []T, f func(T) string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	for _, w := range want {
		if !set[w] {
			t.Fatalf("missing %q in %v", w, got)
		}
	}
}
