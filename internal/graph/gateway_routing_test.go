package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

type fakeModelManager struct {
	models  []gateway.ModelSpec
	routers []gateway.RouterSpec
	testErr error
}

func (f *fakeModelManager) TestConnection(context.Context) error { return f.testErr }
func (f *fakeModelManager) NewModel(_ context.Context, s gateway.ModelSpec) error {
	f.models = append(f.models, s)
	return nil
}
func (f *fakeModelManager) DeleteModel(context.Context, string) error { return nil }
func (f *fakeModelManager) UpsertComplexityRouter(_ context.Context, s gateway.RouterSpec) error {
	f.routers = append(f.routers, s)
	return nil
}

func TestSetRouterTier_SyncsComplexityRouter(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fm := &fakeModelManager{}
	r.GatewayModels = fm
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	if _, err := mr.SetRouterTier(ctx, model.RouterTierLevelSimple, "tier-fast"); err != nil {
		t.Fatalf("SetRouterTier SIMPLE: %v", err)
	}
	if _, err := mr.SetRouterTier(ctx, model.RouterTierLevelReasoning, "tier-reason"); err != nil {
		t.Fatalf("SetRouterTier REASONING: %v", err)
	}
	// re-set SIMPLE to a new model: should update, not duplicate
	if _, err := mr.SetRouterTier(ctx, model.RouterTierLevelSimple, "tier-tiny"); err != nil {
		t.Fatalf("re-set SIMPLE: %v", err)
	}

	tiers, _ := qr.RouterTiers(ctx)
	if len(tiers) != 2 {
		t.Fatalf("expected 2 tiers (SIMPLE updated), got %d", len(tiers))
	}
	last := fm.routers[len(fm.routers)-1]
	if last.ModelName != "smart" {
		t.Fatalf("router model_name = %q", last.ModelName)
	}
	if last.Tiers["SIMPLE"] != "tier-tiny" || last.Tiers["REASONING"] != "tier-reason" {
		t.Fatalf("complexity router tiers not synced: %+v", last.Tiers)
	}
}

func TestUpsertUpstream_SyncsModelWithResolvedKey(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fm := &fakeModelManager{}
	r.GatewayModels = fm
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://up1": {APIKey: "sk-upstream"},
	})
	mr := &mutationResolver{r}

	base := "http://vllm:8000"
	ref := "vault://up1"
	u, err := mr.UpsertUpstream(adminCtx(), model.UpsertUpstreamInput{
		Name: "tier-fast", Provider: model.UpstreamProviderVllm, Model: "openai/qwen-7b",
		APIBase: &base, APIKeyRef: &ref,
	})
	if err != nil {
		t.Fatalf("UpsertUpstream: %v", err)
	}
	if u.Provider != model.UpstreamProviderVllm {
		t.Fatalf("provider = %v", u.Provider)
	}
	if len(fm.models) != 1 {
		t.Fatalf("expected 1 synced model, got %d", len(fm.models))
	}
	if fm.models[0].ModelName != "tier-fast" || fm.models[0].APIKey != "sk-upstream" || fm.models[0].APIBase != base {
		t.Fatalf("model not synced with resolved key: %+v", fm.models[0])
	}
}

func TestRegisterAndTestGatewayConnection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayModels = &fakeModelManager{} // TestConnection returns nil → connected
	mr := &mutationResolver{r}

	g, err := mr.RegisterGatewayConnection(adminCtx(), model.RegisterGatewayConnectionInput{
		Name: "LiteLLM_Router_1", Endpoint: "https://litellm.internal",
	})
	if err != nil {
		t.Fatalf("RegisterGatewayConnection: %v", err)
	}
	st, err := mr.TestGatewayConnection(adminCtx(), g.ID)
	if err != nil {
		t.Fatalf("TestGatewayConnection: %v", err)
	}
	if st != model.GatewayStatusConnected {
		t.Fatalf("status = %v, want connected", st)
	}
	// A successful test from the routing façade must also stamp last_synced_at, so
	// it agrees with the ModelGateway page (shared applyGatewayTestResult helper).
	row := r.Ent.GatewayConnection.GetX(context.Background(), uuid.MustParse(g.ID))
	if row.LastSyncedAt == nil {
		t.Fatal("successful routing test must set last_synced_at")
	}
}

func TestDeleteModelRoute(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}
	ctx := adminCtx()

	mrte, err := mr.UpsertModelRoute(ctx, model.UpsertModelRouteInput{
		Name: "smart", ModelAlias: "smart", Upstreams: []string{"tier-fast"},
	})
	if err != nil {
		t.Fatalf("UpsertModelRoute: %v", err)
	}

	if _, err := mr.DeleteModelRoute(ctx, "not-a-uuid"); err == nil {
		t.Fatal("expected error for invalid id")
	}

	ok, err := mr.DeleteModelRoute(ctx, mrte.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteModelRoute: ok=%v err=%v", ok, err)
	}

	routes, err := qr.ModelRoutes(ctx)
	if err != nil {
		t.Fatalf("ModelRoutes: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("expected route deleted, got %d", len(routes))
	}
}
