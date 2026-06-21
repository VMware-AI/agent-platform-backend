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

func TestCrossTenant_Agents_TenantAdminSeesTenantWide(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	// two agents in tenant A (different owners) + one in tenant B
	r.Ent.Agent.Create().SetName("a1").SetAgentType("goose").SetOwnerUserID(uuid.New()).SetTenantID(tA).SaveX(ctx)
	r.Ent.Agent.Create().SetName("a2").SetAgentType("goose").SetOwnerUserID(uuid.New()).SetTenantID(tA).SaveX(ctx)
	r.Ent.Agent.Create().SetName("b1").SetAgentType("goose").SetOwnerUserID(uuid.New()).SetTenantID(tB).SaveX(ctx)

	qr := &queryResolver{r}
	got, err := qr.Agents(tenantAdminCtx(uuid.NewString(), tA.String()))
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	// tenant-admin sees BOTH of tenant A's agents (tenant-wide, not just owned),
	// and none of tenant B's.
	assertNames(t, names(got, func(a model.Agent) string { return a.Name }), "a1", "a2")
}

func TestCrossTenant_TokenUsage_Strict(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	r.Ent.TokenUsage.Create().SetUserID(uuid.New()).SetModel("m").SetTenantID(tA).SaveX(ctx)
	r.Ent.TokenUsage.Create().SetUserID(uuid.New()).SetModel("m").SetTenantID(tB).SaveX(ctx)
	r.Ent.TokenUsage.Create().SetUserID(uuid.New()).SetModel("m").SaveX(ctx) // platform NULL

	qr := &queryResolver{r}
	got, err := qr.TokenUsage(tenantAdminCtx(uuid.NewString(), tA.String()), nil, nil)
	if err != nil {
		t.Fatalf("TokenUsage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("tenant-admin should see only their tenant's 1 usage row, got %d", len(got))
	}
}

func TestCrossTenant_BClass_ViaParentUser(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()

	// a user in each tenant
	uA := r.Ent.User.Create().SetUsername("ua").SetEmail("ua@x.io").
		SetPasswordHash("h").SetTenantID(tA).SaveX(ctx)
	uB := r.Ent.User.Create().SetUsername("ub").SetEmail("ub@x.io").
		SetPasswordHash("h").SetTenantID(tB).SaveX(ctx)

	// VirtualKey via user
	r.Ent.VirtualKey.Create().SetLitellmKey("ka").SetUserID(uA.ID).SaveX(ctx)
	r.Ent.VirtualKey.Create().SetLitellmKey("kb").SetUserID(uB.ID).SaveX(ctx)
	// RequestLog via user
	r.Ent.RequestLog.Create().SetRequestID("ra").SetUserID(uA.ID).SaveX(ctx)
	r.Ent.RequestLog.Create().SetRequestID("rb").SetUserID(uB.ID).SaveX(ctx)
	// AuditLog via actor
	r.Ent.AuditLog.Create().SetAction("a.act").SetActorUserID(uA.ID).SaveX(ctx)
	r.Ent.AuditLog.Create().SetAction("b.act").SetActorUserID(uB.ID).SaveX(ctx)

	qr := &queryResolver{r}
	taCtx := tenantAdminCtx(uuid.NewString(), tA.String())

	keys, err := qr.VirtualKeys(taCtx, nil)
	if err != nil {
		t.Fatalf("VirtualKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].UserID != uA.ID.String() {
		t.Fatalf("tenant-admin should see only tenant A's key, got %d", len(keys))
	}

	logs, err := qr.RequestLogs(taCtx, nil, nil)
	if err != nil {
		t.Fatalf("RequestLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].RequestID != "ra" {
		t.Fatalf("tenant-admin should see only tenant A's request log, got %d", len(logs))
	}

	audit, err := qr.AuditLogs(taCtx, nil, nil)
	if err != nil {
		t.Fatalf("AuditLogs: %v", err)
	}
	if audit.Total != 1 || len(audit.Items) != 1 || audit.Items[0].Action != "a.act" {
		t.Fatalf("tenant-admin should see only tenant A's audit (total=%d items=%d)", audit.Total, len(audit.Items))
	}
}

// AC-8: two tenants may hold an artifact of the same name+version (per-tenant
// unique index); within one tenant it stays unique (upsert updates in place).
func TestCrossTenant_SameNameAcrossTenants(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()
	mr := &mutationResolver{r}

	mkInput := model.UpsertArtifactInput{Name: "shared", Kind: model.ArtifactKindConfig, Version: "1", URI: "u"}
	if _, err := mr.UpsertArtifact(tenantUserCtx(uuid.NewString(), tA.String()), mkInput); err != nil {
		t.Fatalf("tenant A upsert: %v", err)
	}
	if _, err := mr.UpsertArtifact(tenantUserCtx(uuid.NewString(), tB.String()), mkInput); err != nil {
		t.Fatalf("tenant B upsert (same name) should be allowed: %v", err)
	}
	// two distinct rows now share name "shared"
	if n := r.Ent.Artifact.Query().CountX(ctx); n != 2 {
		t.Fatalf("expected 2 same-named artifacts across tenants, got %d", n)
	}
	// re-upsert within tenant A updates in place (no third row)
	if _, err := mr.UpsertArtifact(tenantUserCtx(uuid.NewString(), tA.String()), mkInput); err != nil {
		t.Fatalf("tenant A re-upsert: %v", err)
	}
	if n := r.Ent.Artifact.Query().CountX(ctx); n != 2 {
		t.Fatalf("re-upsert within tenant should not add a row, got %d", n)
	}
}
