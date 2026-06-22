package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/httpx"
)

// TestEnvScope_Filtering verifies LLD-10 §2.3 / AC-9: when env_scope is enabled
// and the request carries X-Environment, a list shows only rows in that env plus
// env-NULL (tenant-level) rows; disabling the flag or omitting the header is a
// no-op (no env filter). Uses admin context to isolate the env filter from
// tenant scoping.
func TestEnvScope_Filtering(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	envA, envB := uuid.New(), uuid.New()

	r.Ent.Artifact.Create().SetName("ea").SetKind("config").SetVersion("1").SetURI("u").SetEnvironmentID(envA).SaveX(ctx)
	r.Ent.Artifact.Create().SetName("eb").SetKind("config").SetVersion("1").SetURI("u").SetEnvironmentID(envB).SaveX(ctx)
	r.Ent.Artifact.Create().SetName("enull").SetKind("config").SetVersion("1").SetURI("u").SaveX(ctx) // tenant-level

	qr := &queryResolver{r}
	nm := func(xs []model.Artifact) []string { return names(xs, func(a model.Artifact) string { return a.Name }) }

	// enabled + X-Environment=A → envA + envNULL (not envB)
	r.EnvScopeEnabled = true
	got, err := qr.Artifacts(httpx.WithEnvironment(adminCtx(), envA), nil)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	assertNames(t, nm(got), "ea", "enull")

	// enabled but NO X-Environment → no env filter (all 3)
	if all, _ := qr.Artifacts(adminCtx(), nil); len(all) != 3 {
		t.Fatalf("no X-Environment should not filter, got %d", len(all))
	}

	// disabled flag → no env filter even with header (all 3)
	r.EnvScopeEnabled = false
	if all, _ := qr.Artifacts(httpx.WithEnvironment(adminCtx(), envA), nil); len(all) != 3 {
		t.Fatalf("env_scope disabled should not filter, got %d", len(all))
	}
}

// TestEnvScope_TenantHardBeforeEnvSoft: env is a soft boundary applied AFTER the
// hard tenant boundary — a row in another tenant is never visible regardless of
// env (AC-9 "tenant 先挡").
func TestEnvScope_TenantHardBeforeEnvSoft(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.EnvScopeEnabled = true
	ctx := context.Background()
	tA, tB := uuid.New(), uuid.New()
	env := uuid.New()

	// tenant B artifact tagged with env that tenant A's request will ask for
	r.Ent.Artifact.Create().SetName("b-in-env").SetKind("config").SetVersion("1").SetURI("u").
		SetTenantID(tB).SetEnvironmentID(env).SaveX(ctx)
	r.Ent.Artifact.Create().SetName("a-in-env").SetKind("config").SetVersion("1").SetURI("u").
		SetTenantID(tA).SetEnvironmentID(env).SaveX(ctx)

	got, err := (&queryResolver{r}).Artifacts(httpx.WithEnvironment(tenantUserCtx(uuid.NewString(), tA.String()), env), nil)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	// only tenant A's row — tenant B's stays hidden though it matches the env
	assertNames(t, names(got, func(a model.Artifact) string { return a.Name }), "a-in-env")
}
