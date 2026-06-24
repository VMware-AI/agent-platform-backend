package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/upstream"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
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
	// per-gateway client builder (H1): fake → TestConnection nil → connected
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
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

	// a failing gateway → ERROR status, summary FAILED (only this one gateway)
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("dial tcp: refused")}
	}
	res2, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("test (fail path): %v", err)
	}
	// H2: the raw transport error must NOT leak to the client message.
	if res2.Message != "connection failed" {
		t.Fatalf("error message should be sanitized, got %q", res2.Message)
	}
	sum2, _ := qr.ModelGatewaySyncSummary(ctx)
	if sum2.State != model.ModelGatewaySyncStateFailed || sum2.FailedCount != 1 {
		t.Fatalf("sync summary after failure: %+v", sum2)
	}
}

// M1: the sync-summary state machine must be total over connected/error/disconnected.
func TestModelGatewaySyncSummary_StateMachine(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// no gateways → NEVER
	if s, _ := qr.ModelGatewaySyncSummary(ctx); s.State != model.ModelGatewaySyncStateNever {
		t.Fatalf("empty fleet: %v, want NEVER", s.State)
	}

	// two fresh (disconnected, never tested) gateways → NEVER, not PARTIAL/SYNCED
	a := mkGateway(t, mr, ctx, "a", "https://a:4000")
	mkGateway(t, mr, ctx, "b", "https://b:4000")
	if s, _ := qr.ModelGatewaySyncSummary(ctx); s.State != model.ModelGatewaySyncStateNever {
		t.Fatalf("all-disconnected: %v, want NEVER", s.State)
	}

	// connect only one → mix of connected + disconnected → PARTIAL (the bug M1 fixed)
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	if _, err := mr.TestModelGatewayConnection(ctx, a.ID); err != nil {
		t.Fatalf("test a: %v", err)
	}
	s, _ := qr.ModelGatewaySyncSummary(ctx)
	if s.State != model.ModelGatewaySyncStatePartial || s.SuccessCount != 1 {
		t.Fatalf("connected+disconnected: %+v, want PARTIAL/success=1", s)
	}
}

// M4: adminUrl is persisted when set, and derived from the endpoint when not.
func TestModelGateway_AdminURLPersistedOrDerived(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	custom := "https://litellm.internal/admin"
	withURL, err := mr.CreateModelGateway(ctx, model.ModelGatewayInput{
		Name: "with", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://gw:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin, AdminURL: &custom,
	})
	if err != nil || withURL.AdminURL == nil || *withURL.AdminURL != custom {
		t.Fatalf("adminUrl not persisted: %+v / %v", withURL.AdminURL, err)
	}
	without, _ := mr.CreateModelGateway(ctx, model.ModelGatewayInput{
		Name: "without", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://gw2:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	})
	if without.AdminURL == nil || *without.AdminURL != "https://gw2:4000/ui" {
		t.Fatalf("adminUrl not derived: %+v", without.AdminURL)
	}
}

// M2/M3: lastSyncAt is nil until a successful test, is set on success, and does
// NOT move on an unrelated update.
func TestModelGateway_LastSyncTracking(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "g", "https://gw:4000")
	if g.LastSyncAt != nil {
		t.Fatal("fresh gateway should have nil lastSyncAt")
	}

	res, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil || res.Gateway.LastSyncAt == nil {
		t.Fatalf("successful test must set lastSyncAt: %+v / %v", res.Gateway.LastSyncAt, err)
	}
	synced := *res.Gateway.LastSyncAt

	// an unrelated update must NOT move lastSyncAt (M3 fix)
	upd, err := mr.UpdateModelGateway(ctx, g.ID, model.ModelGatewayInput{
		Name: "renamed", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://gw:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	})
	if err != nil || upd.LastSyncAt == nil || !upd.LastSyncAt.Equal(synced) {
		t.Fatalf("update must not change lastSyncAt: got %v, want %v", upd.LastSyncAt, synced)
	}

	// summary surfaces the real sync time
	sum, _ := qr.ModelGatewaySyncSummary(ctx)
	if sum.LastSyncedAt == nil || !sum.LastSyncedAt.Equal(synced) {
		t.Fatalf("summary lastSyncedAt = %v, want %v", sum.LastSyncedAt, synced)
	}
}

// M2/M3 invariant: a FAILED test must not clear or move a prior lastSyncAt.
func TestModelGateway_FailedTestKeepsSyncTime(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	// first test succeeds → sets lastSyncAt
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	g := mkGateway(t, mr, ctx, "g", "https://gw:4000")
	good, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil || good.Gateway.LastSyncAt == nil {
		t.Fatalf("first test should set lastSyncAt: %+v / %v", good.Gateway.LastSyncAt, err)
	}
	synced := *good.Gateway.LastSyncAt

	// second test FAILS → status ERROR, but lastSyncAt preserved
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("down")}
	}
	bad, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("test (fail): %v", err)
	}
	if bad.Gateway.Status != model.ModelGatewayStatusError {
		t.Fatalf("status = %v, want ERROR", bad.Gateway.Status)
	}
	if bad.Gateway.LastSyncAt == nil || !bad.Gateway.LastSyncAt.Equal(synced) {
		t.Fatalf("failed test must keep old lastSyncAt: got %v, want %v", bad.Gateway.LastSyncAt, synced)
	}
}

// H1: the connection test must build a client from the gateway's OWN endpoint and
// its OWN master key (resolved from the secret store), not a process-wide default.
func TestModelGateway_TestUsesPerGatewayClient(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.Secrets = secrets.NewStaticResolver(nil) // a Store: accepts the masterKey on create
	ctx := adminCtx()
	mr := &mutationResolver{r}

	var gotEndpoint, gotKey string
	r.GatewayClientFor = func(_ context.Context, endpoint, masterKey string) gateway.ModelManager {
		gotEndpoint, gotKey = endpoint, masterKey
		return &fakeModelManager{}
	}

	g, err := mr.CreateModelGateway(ctx, model.ModelGatewayInput{
		Name: "gw", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://vc-x:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin, MasterKey: ptr("sk-secret-xyz"),
	})
	if err != nil {
		t.Fatalf("CreateModelGateway: %v", err)
	}
	if _, err := mr.TestModelGatewayConnection(ctx, g.ID); err != nil {
		t.Fatalf("TestModelGatewayConnection: %v", err)
	}
	if gotEndpoint != "https://vc-x:4000" {
		t.Errorf("per-gateway endpoint = %q, want https://vc-x:4000", gotEndpoint)
	}
	if gotKey != "sk-secret-xyz" {
		t.Errorf("master key not resolved from secret store: %q", gotKey)
	}
}

// H1 negative path: a gateway with no master key (and no secret store) tests with
// an empty key rather than crashing.
func TestModelGateway_TestEmptyKeyWhenNoSecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// r.Secrets left nil; gateway created without MasterKey
	ctx := adminCtx()
	mr := &mutationResolver{r}
	var gotKey = "sentinel"
	r.GatewayClientFor = func(_ context.Context, _, masterKey string) gateway.ModelManager {
		gotKey = masterKey
		return &fakeModelManager{}
	}
	g := mkGateway(t, mr, ctx, "gw", "https://llm:4000")
	if _, err := mr.TestModelGatewayConnection(ctx, g.ID); err != nil {
		t.Fatalf("test: %v", err)
	}
	if gotKey != "" {
		t.Errorf("master key = %q, want empty (no secret store, no ref)", gotKey)
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
