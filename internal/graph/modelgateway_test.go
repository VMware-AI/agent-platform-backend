package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/upstream"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// mkGateway registers a model gateway via the resolver and returns it (P4 helper).
func mkGateway(t *testing.T, mr *mutationResolver, ctx context.Context, name, endpoint string) *model.ModelGateway {
	t.Helper()
	g, err := mr.CreateModelGateway(ctx, model.ModelGatewayInput{
		Name:                  name,
		Provider:              model.ModelGatewayProviderLitellm,
		Endpoint:              endpoint,
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	})
	if err != nil {
		t.Fatalf("CreateModelGateway(%s): %v", name, err)
	}
	return g
}

// modelGateways projects GatewayConnection into the console aggregate: fixed
// provider/strategy, derived adminUrl, real backendModelCount, paged totalCount.
func TestModelGateways_Projection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "litellm-prod", "https://llm.internal:4000")
	if g.Provider != model.ModelGatewayProviderLitellm ||
		g.LoadBalancingStrategy != model.LoadBalancingStrategyRoundRobin ||
		g.Status != model.ModelGatewayStatusDisconnected {
		t.Fatalf("projection defaults wrong: %+v", g)
	}
	if g.AdminURL == nil || *g.AdminURL != "https://llm.internal:4000/ui" {
		t.Fatalf("adminUrl not derived: %v", g.AdminURL)
	}
	if g.LastSyncAt != nil || g.LastSyncStatus != model.ModelGatewaySyncStateNever {
		t.Fatalf("fresh gateway should be NEVER-synced: %+v", g)
	}

	// backendModelCount reflects the real upstream count
	for i := 0; i < 2; i++ {
		r.Ent.Upstream.Create().SetName("u" + string(rune('a'+i))).
			SetProvider(upstream.ProviderVllm).SetModel("m").SaveX(context.Background())
	}
	conn, err := qr.ModelGateways(ctx, nil, model.PageInput{})
	if err != nil {
		t.Fatalf("ModelGateways: %v", err)
	}
	if conn.TotalCount != 1 || len(conn.Nodes) != 1 || conn.Nodes[0].BackendModelCount != 2 {
		t.Fatalf("backendModelCount/total wrong: total=%d count=%d", conn.TotalCount, conn.Nodes[0].BackendModelCount)
	}

	// search filter
	hit, _ := qr.ModelGateways(ctx, &model.ModelGatewayFilterInput{Search: ptr("prod")}, model.PageInput{})
	if hit.TotalCount != 1 {
		t.Fatalf("search prod: %d, want 1", hit.TotalCount)
	}
	miss, _ := qr.ModelGateways(ctx, &model.ModelGatewayFilterInput{Search: ptr("nope")}, model.PageInput{})
	if miss.TotalCount != 0 {
		t.Fatalf("search nope: %d, want 0", miss.TotalCount)
	}
}

// testModelGatewayConnection pings live litellm, flips status, returns latency;
// the sync summary then reports the aggregate.
func TestModelGateway_TestAndSyncSummary(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayModels = &fakeModelManager{} // TestConnection → nil → connected
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "g1", "https://llm:4000")
	res, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("TestModelGatewayConnection: %v", err)
	}
	if !res.Success || res.Status != model.ModelGatewayStatusConnected || res.LatencyMs == nil {
		t.Fatalf("test result wrong: %+v", res)
	}
	if res.Gateway.Status != model.ModelGatewayStatusConnected || res.Gateway.LastSyncStatus != model.ModelGatewaySyncStateSynced {
		t.Fatalf("gateway not flipped to connected/synced: %+v", res.Gateway)
	}

	sum, err := qr.ModelGatewaySyncSummary(ctx)
	if err != nil {
		t.Fatalf("ModelGatewaySyncSummary: %v", err)
	}
	if sum.State != model.ModelGatewaySyncStateSynced || sum.SuccessCount != 1 || sum.FailedCount != 0 || sum.LastSyncedAt == nil {
		t.Fatalf("sync summary wrong: %+v", sum)
	}

	// a failing gateway → ERROR status, summary FAILED
	r.GatewayModels = &fakeModelManager{testErr: errors.New("dial tcp: refused")}
	if _, err := mr.TestModelGatewayConnection(ctx, g.ID); err != nil {
		t.Fatalf("test (fail path): %v", err)
	}
	sum2, _ := qr.ModelGatewaySyncSummary(ctx)
	if sum2.State != model.ModelGatewaySyncStateFailed || sum2.FailedCount != 1 {
		t.Fatalf("sync summary after failure: %+v", sum2)
	}
}

// update edits name/endpoint; delete returns the id and removes the row.
func TestModelGateway_UpdateDelete(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "old", "https://old:4000")
	upd, err := mr.UpdateModelGateway(ctx, g.ID, model.ModelGatewayInput{
		Name: "new", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://new:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	})
	if err != nil || upd.Name != "new" || upd.Endpoint != "https://new:4000" {
		t.Fatalf("update: %+v / %v", upd, err)
	}

	del, err := mr.DeleteModelGateway(ctx, g.ID)
	if err != nil || del.DeletedID != g.ID {
		t.Fatalf("delete: %+v / %v", del, err)
	}
	conn, _ := qr.ModelGateways(ctx, nil, model.PageInput{})
	if conn.TotalCount != 0 {
		t.Fatalf("gateway should be deleted, got %d", conn.TotalCount)
	}
}
