package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
)

// Cross-tenant write-guard regressions for #30 (租户越权 medium). Each asserts that
// a tenant-scoped mutation cannot reach across the tenant boundary — either reads
// as missing (404 oracle) or leaves another tenant's state untouched.

const (
	tenantWriteA = "1a1a1a1a-1a1a-1a1a-1a1a-1a1a1a1a1a1a"
	tenantWriteB = "2b2b2b2b-2b2b-2b2b-2b2b-2b2b2b2b2b2b"
)

// A tenant-admin must not toggle another tenant's rate-limit policy: it reads as
// missing (no existence oracle), mirroring DeleteRateLimitPolicy.
func TestSetRateLimitPolicyEnabled_CrossTenantDenied(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	ctxA := tenantAdminCtx("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", tenantWriteA)
	ctxB := tenantAdminCtx("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", tenantWriteB)

	rpm := 30
	enabled := false
	polA, err := mr.UpsertRateLimitPolicy(ctxA, model.UpsertRateLimitPolicyInput{
		Name: "tenant-a-limit", Rpm: &rpm, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("UpsertRateLimitPolicy (tenant A): %v", err)
	}

	// Tenant B tries to flip tenant A's policy on — must be refused as not-found.
	if _, err := mr.SetRateLimitPolicyEnabled(ctxB, polA.ID, true); err == nil {
		t.Fatal("tenant B toggled tenant A's policy; expected not-found")
	}

	// The policy stayed disabled — the cross-tenant write had no effect.
	pid := uuid.MustParse(polA.ID)
	got, err := r.Ent.RateLimitPolicy.Get(ctxA, pid)
	if err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if got.Enabled {
		t.Fatal("policy was enabled by a cross-tenant caller")
	}
}

// CreateAgentConfig's "unset previous default" must be scoped to the creating
// tenant: a new default in tenant B must not clear tenant A's default for the
// same agent type.
func TestCreateAgentConfig_DefaultNoCrossTenantClobber(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	ctxA := tenantAdminCtx("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", tenantWriteA)
	ctxB := tenantAdminCtx("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", tenantWriteB)

	yes := true
	cA, err := mr.CreateAgentConfig(ctxA, model.CreateAgentConfigInput{Name: "a-goose", AgentType: "goose", IsDefault: &yes})
	if err != nil || !cA.IsDefault {
		t.Fatalf("create tenant-A default: %v default=%v", err, cA)
	}
	if _, err := mr.CreateAgentConfig(ctxB, model.CreateAgentConfigInput{Name: "b-goose", AgentType: "goose", IsDefault: &yes}); err != nil {
		t.Fatalf("create tenant-B default: %v", err)
	}

	// Tenant A's default must survive tenant B's create.
	if !reloadAgentConfigIsDefault(t, r, cA.ID) {
		t.Fatal("tenant A's default was cleared by a tenant-B create")
	}
}

// SetDefaultAgentConfig's unset must likewise stay within the caller's tenant: a
// dept default flip in tenant A leaves tenant B's same-type default in place.
func TestSetDefaultAgentConfig_NoCrossTenantClobber(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}

	ctxA := tenantAdminCtx("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", tenantWriteA)
	ctxB := tenantAdminCtx("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", tenantWriteB)

	yes := true
	no := false
	// Tenant A: a default + a non-default of the same type.
	aDefault, err := mr.CreateAgentConfig(ctxA, model.CreateAgentConfigInput{Name: "a-1", AgentType: "goose", IsDefault: &yes})
	if err != nil {
		t.Fatalf("create a-1: %v", err)
	}
	aOther, err := mr.CreateAgentConfig(ctxA, model.CreateAgentConfigInput{Name: "a-2", AgentType: "goose", IsDefault: &no})
	if err != nil {
		t.Fatalf("create a-2: %v", err)
	}
	// Tenant B: its own default of the same type.
	bDefault, err := mr.CreateAgentConfig(ctxB, model.CreateAgentConfigInput{Name: "b-1", AgentType: "goose", IsDefault: &yes})
	if err != nil {
		t.Fatalf("create b-1: %v", err)
	}

	// Tenant A flips its default to the other config.
	if _, err := mr.SetDefaultAgentConfig(ctxA, aOther.ID); err != nil {
		t.Fatalf("SetDefaultAgentConfig (tenant A): %v", err)
	}

	// Within tenant A the default moved; tenant B's default is untouched.
	if reloadAgentConfigIsDefault(t, r, aDefault.ID) {
		t.Fatal("tenant A old default not cleared")
	}
	if !reloadAgentConfigIsDefault(t, r, aOther.ID) {
		t.Fatal("tenant A new default not set")
	}
	if !reloadAgentConfigIsDefault(t, r, bDefault.ID) {
		t.Fatal("tenant B's default was cleared by a tenant-A flip")
	}
}

func reloadAgentConfigIsDefault(t *testing.T, r *Resolver, id string) bool {
	t.Helper()
	cid := uuid.MustParse(id)
	cfg, err := r.Ent.AgentConfig.Get(adminCtx(), cid)
	if err != nil {
		t.Fatalf("reload agent config %s: %v", id, err)
	}
	return cfg.IsDefault
}
