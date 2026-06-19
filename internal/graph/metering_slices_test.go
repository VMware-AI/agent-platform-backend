package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestMeteringSummary_ByAgentAndByDate(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	uid := "11111111-1111-1111-1111-111111111111"
	agentA := "22222222-2222-2222-2222-222222222222"
	agentB := "33333333-3333-3333-3333-333333333333"
	for _, rec := range []model.RecordTokenUsageInput{
		{UserID: uid, AgentID: &agentA, Model: "tier-fast", InputTokens: 100, OutputTokens: 200},
		{UserID: uid, AgentID: &agentA, Model: "tier-fast", InputTokens: 50, OutputTokens: 60},
		{UserID: uid, AgentID: &agentB, Model: "tier-heavy", InputTokens: 300, OutputTokens: 400},
	} {
		if _, err := mr.RecordTokenUsage(ctx, rec); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	sum, err := qr.MeteringSummary(ctx, &uid)
	if err != nil {
		t.Fatalf("MeteringSummary: %v", err)
	}
	if len(sum.ByAgent) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(sum.ByAgent))
	}
	var a *model.AgentUsage
	for i := range sum.ByAgent {
		if sum.ByAgent[i].AgentID == agentA {
			a = &sum.ByAgent[i]
		}
	}
	if a == nil || a.InputTokens != 150 || a.OutputTokens != 260 {
		t.Fatalf("agentA aggregate wrong: %+v", a)
	}
	// all records same day (default created_at) => 1 date bucket with the totals
	if len(sum.ByDate) != 1 {
		t.Fatalf("expected 1 date bucket, got %d", len(sum.ByDate))
	}
	if sum.ByDate[0].InputTokens != 450 {
		t.Fatalf("byDate total input = %d, want 450", sum.ByDate[0].InputTokens)
	}
}

func TestUpsertModelRoute_BackendGateway(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}

	g, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "LiteLLM_Router_1", Endpoint: "https://litellm",
	})
	if err != nil {
		t.Fatalf("register gateway: %v", err)
	}
	route, err := mr.UpsertModelRoute(ctx, model.UpsertModelRouteInput{
		Name: "global_litellm_router", ModelAlias: "gpt-4o", BackendGatewayID: &g.ID,
		Upstreams: []string{"tier-fast"},
	})
	if err != nil {
		t.Fatalf("upsert route: %v", err)
	}
	if route.BackendGatewayID == nil || *route.BackendGatewayID != g.ID {
		t.Fatalf("backend gateway not linked: %+v", route.BackendGatewayID)
	}
}
