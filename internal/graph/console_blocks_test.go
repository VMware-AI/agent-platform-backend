package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestCreateAndUpdateModelRoute covers the console 模型路由 CRUD (create by id, then
// partial update), distinct from the name-keyed upsert.
func TestCreateAndUpdateModelRoute(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	strat := model.ModelRouteStrategyWeightedRoundRobin
	created, err := mr.CreateModelRoute(ctx, model.CreateModelRouteInput{
		Name:            "global_litellm_router",
		GatewayName:     ptr("LiteLLM_Router_1"),
		SupportedModels: []string{"gpt-4o", "llama-3-70b"},
		UIStrategy:      &strat,
		Enabled:         ptr(true),
	})
	if err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}
	if created.GatewayName != "LiteLLM_Router_1" {
		t.Fatalf("gatewayName = %q", created.GatewayName)
	}
	if len(created.SupportedModels) != 2 || created.SupportedModels[0] != "gpt-4o" {
		t.Fatalf("supportedModels = %v", created.SupportedModels)
	}
	if created.UIStrategy != model.ModelRouteStrategyWeightedRoundRobin {
		t.Fatalf("uiStrategy = %v", created.UIStrategy)
	}
	if !created.Enabled {
		t.Fatal("expected enabled")
	}

	// Partial update: change models + strategy, leave name/gateway untouched.
	newStrat := model.ModelRouteStrategyRandom
	updated, err := mr.UpdateModelRoute(ctx, created.ID, model.UpdateModelRouteInput{
		SupportedModels: []string{"gpt-4o-mini"},
		UIStrategy:      &newStrat,
	})
	if err != nil {
		t.Fatalf("UpdateModelRoute: %v", err)
	}
	if len(updated.SupportedModels) != 1 || updated.SupportedModels[0] != "gpt-4o-mini" {
		t.Fatalf("updated supportedModels = %v", updated.SupportedModels)
	}
	if updated.UIStrategy != model.ModelRouteStrategyRandom {
		t.Fatalf("updated uiStrategy = %v", updated.UIStrategy)
	}
	if updated.Name != "global_litellm_router" {
		t.Fatalf("name should be unchanged: %q", updated.Name)
	}

	// setEnabled + delete round-trip through the list.
	if _, err := mr.SetModelRouteEnabled(ctx, created.ID, false); err != nil {
		t.Fatalf("SetModelRouteEnabled: %v", err)
	}
	routes, err := qr.ModelRoutes(ctx)
	if err != nil {
		t.Fatalf("ModelRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Enabled {
		t.Fatalf("expected 1 disabled route, got %+v", routes)
	}
	if ok, err := mr.DeleteModelRoute(ctx, created.ID); err != nil || !ok {
		t.Fatalf("DeleteModelRoute: ok=%v err=%v", ok, err)
	}
}

// TestMeteringOverview aggregates per-agent/model/day with request counts, resolves
// agent names, totals, and the default LAST_7_DAYS range.
func TestMeteringOverview(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// A real agent so byAgent rows carry a display name (not just the id).
	ag, err := r.Ent.Agent.Create().
		SetName("claw-agent-v1").
		SetAgentType("openclaw").
		SetOwnerUserID(uuid.New()).
		Save(ctx)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentID := ag.ID.String()
	uid := uuid.NewString()

	for _, rec := range []model.RecordTokenUsageInput{
		{UserID: uid, AgentID: &agentID, Model: "gpt-4o", InputTokens: 100, OutputTokens: 200, Cost: ptrF(1.5)},
		{UserID: uid, AgentID: &agentID, Model: "gpt-4o", InputTokens: 50, OutputTokens: 60, Cost: ptrF(0.5)},
		{UserID: uid, Model: "gpt-4o-mini", InputTokens: 10, OutputTokens: 20, Cost: ptrF(0.1)},
	} {
		if _, err := mr.RecordTokenUsage(ctx, rec); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	ov, err := qr.MeteringOverview(ctx, nil, nil) // default range
	if err != nil {
		t.Fatalf("MeteringOverview: %v", err)
	}
	if ov.Range != model.MeteringTimeRangeLast7Days {
		t.Fatalf("default range = %v", ov.Range)
	}
	if ov.TotalRequests != 3 {
		t.Fatalf("totalRequests = %d, want 3", ov.TotalRequests)
	}
	if ov.TotalInputTokens != 160 || ov.TotalOutputTokens != 280 || ov.TotalTokens != 440 {
		t.Fatalf("totals wrong: %+v", ov)
	}
	if len(ov.ByAgent) != 1 {
		t.Fatalf("expected 1 agent row, got %d", len(ov.ByAgent))
	}
	if ov.ByAgent[0].AgentName != "claw-agent-v1" {
		t.Fatalf("agentName = %q", ov.ByAgent[0].AgentName)
	}
	if ov.ByAgent[0].Requests != 2 || ov.ByAgent[0].TotalTokens != 410 {
		t.Fatalf("agent row wrong: %+v", ov.ByAgent[0])
	}
	if len(ov.ByModel) != 2 {
		t.Fatalf("expected 2 model rows, got %d", len(ov.ByModel))
	}
	if len(ov.ByDay) != 1 {
		t.Fatalf("expected 1 day bucket, got %d", len(ov.ByDay))
	}
	if ov.Cost == nil || ov.Cost.TotalCost == 0 || ov.Cost.MonthlyCost == 0 {
		t.Fatalf("cost summary wrong: %+v", ov.Cost)
	}
}

func ptrF(f float64) *float64 { return &f }
