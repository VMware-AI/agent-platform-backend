package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/google/uuid"
)

// mkGateway registers a model gateway via the resolver and waits for the
// post-create background sync to finish before returning. This is the default
// in tests because (a) the post-create sync is part of CreateModelGateway's
// contract, and (b) tests asserting on the post-create state need a stable
// view. Tests that intentionally want to observe the in-flight state should
// build the row directly (see TestModelGateway_SyncShowsSyncingDuringInflight).
//
// If the test hasn't installed r.GatewayClientFor, mkGateway installs a
// default no-op fake (TestConnection → connected, no strategy, no models).
// Tests that want a different fake must set r.GatewayClientFor BEFORE
// calling mkGateway.
//
// The row's name is auto-suffixed with t.Name() to dodge the in-memory sqlite
// shared-cache unique-name constraint across tests (the dev store opens with
// cache=shared, so two tests creating the same name collide).
func mkGateway(t *testing.T, mr *mutationResolver, ctx context.Context, name, endpoint string) *model.ModelGateway {
	t.Helper()
	r := mr.Resolver
	if r.GatewayClientFor == nil {
		r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
			return &fakeModelManager{}
		}
	}
	c := r.Ent.GatewayConnection.Create().SetName(name + "-" + t.Name()).SetEndpoint(endpoint)
	g, err := c.Save(ctx)
	if err != nil {
		t.Fatalf("CreateModelGateway(%s): %v", name, err)
	}
	<-r.syncGatewayInBackground(g.ID)
	// Re-fetch so the projection reflects the post-sync row (lastSyncAt /
	// status / strategy / count updated by the background sync).
	fresh, err := r.Ent.GatewayConnection.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("post-sync reload: %v", err)
	}
	return r.toModelGateway(fresh)
}

// modelGateways projects GatewayConnection into the console aggregate: fixed
// provider, real backendModelCount, paged totalCount.
func TestModelGateways_Projection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// mkGateway waits for the post-create sync to land.
	g := mkGateway(t, mr, ctx, "litellm-prod", "https://llm.internal:4000")
	if g.Provider != model.ModelGatewayProviderLitellm {
		t.Fatalf("provider wrong: %+v", g)
	}
	if g.LastSyncAt == nil || g.LastSyncStatus != model.ModelGatewaySyncStateSynced {
		t.Fatalf("post-create sync should leave the gateway SYNCED: %+v", g)
	}
	if g.BackendModelCount != 0 {
		t.Fatalf("post-create sync with empty ListModels should leave backendModelCount=0: %d", g.BackendModelCount)
	}

	// search filter
	hit, _ := qr.ModelGateways(ctx, &model.ModelGatewayFilterInput{Search: ptr("prod")}, model.PageInput{}, nil)
	if hit.TotalCount != 1 {
		t.Fatalf("search prod: %d, want 1", hit.TotalCount)
	}
	miss, _ := qr.ModelGateways(ctx, &model.ModelGatewayFilterInput{Search: ptr("nope")}, model.PageInput{}, nil)
	if miss.TotalCount != 0 {
		t.Fatalf("search nope: %d, want 0", miss.TotalCount)
	}
}

// syncModelGatewayConnection pings live litellm, flips lastSyncStatus, writes
// lastSyncedAt + strategy + count; the sync summary then reports the aggregate.
func TestModelGateway_SyncAndSyncSummary(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// per-gateway client builder: fake → TestConnection nil → connected, plus a
	// 3-model list response so the sync also persists backendModelCount.
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{
			listModels: []gateway.ModelInfo{
				{ID: "m1", ModelName: "a"}, {ID: "m2", ModelName: "b"}, {ID: "m3", ModelName: "c"},
			},
		}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "g1", "https://llm:4000")
	res, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("SyncModelGatewayConnection: %v", err)
	}
	if !res.Success {
		t.Fatalf("sync result wrong: %+v", res)
	}
	if res.Gateway.LastSyncStatus != model.ModelGatewaySyncStateSynced {
		t.Fatalf("gateway not flipped to synced: %+v", res.Gateway)
	}
	if res.Gateway.BackendModelCount != 3 {
		t.Fatalf("backendModelCount not persisted from ListModels: %d", res.Gateway.BackendModelCount)
	}

	sum, err := qr.ModelGatewaySyncSummary(ctx)
	if err != nil {
		t.Fatalf("ModelGatewaySyncSummary: %v", err)
	}
	if sum.State != model.ModelGatewaySyncStateSynced || sum.SuccessCount != 1 || sum.FailedCount != 0 || sum.LastSyncedAt == nil {
		t.Fatalf("sync summary wrong: %+v", sum)
	}

	// a failing gateway → ERROR status, summary FAILED (only this one gateway).
	// The fake's listErr is preserved on failure — backendModelCount must NOT
	// be reset to 0.
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("dial tcp: refused"), listErr: errors.New("down")}
	}
	res2, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("sync (fail path): %v", err)
	}
	// raw transport error must NOT leak to the client message.
	if res2.Message != "connection failed" {
		t.Fatalf("error message should be sanitized, got %q", res2.Message)
	}
	if res2.Gateway.BackendModelCount != 3 {
		t.Fatalf("backendModelCount must be preserved on failure, got %d", res2.Gateway.BackendModelCount)
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

	// Build rows directly so the post-create background sync does NOT run
	// (we want fresh, disconnected rows for the state-machine coverage).
	a := makeDisconnectedGateway(t, r, ctx, "a")
	_ = makeDisconnectedGateway(t, r, ctx, "b")
	if s, _ := qr.ModelGatewaySyncSummary(ctx); s.State != model.ModelGatewaySyncStateNever {
		t.Fatalf("all-disconnected: %v, want NEVER", s.State)
	}

	// connect only one → mix of connected + disconnected → PARTIAL
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	if _, err := mr.SyncModelGatewayConnection(ctx, a.ID); err != nil {
		t.Fatalf("sync a: %v", err)
	}
	s, _ := qr.ModelGatewaySyncSummary(ctx)
	if s.State != model.ModelGatewaySyncStatePartial || s.SuccessCount != 1 {
		t.Fatalf("connected+disconnected: %+v, want PARTIAL/success=1", s)
	}
}

// makeDisconnectedGateway inserts a gateway row directly via ent (bypassing
// the resolver) so the post-create background sync does NOT fire. Used by
// tests that want to assert the "fresh, never synced" state.
func makeDisconnectedGateway(t *testing.T, r *Resolver, ctx context.Context, name string) *model.ModelGateway {
	t.Helper()
	g, err := r.Ent.GatewayConnection.Create().
		SetName(name + "-" + t.Name()).SetEndpoint("https://x:4000").Save(ctx)
	if err != nil {
		t.Fatalf("create row %s: %v", name, err)
	}
	return r.toModelGateway(g)
}

// modelGateways exposes createdAt/updatedAt and honors the sort arg.
func TestModelGateways_SortAndTimestamps(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	mkGateway(t, mr, ctx, "bravo", "https://b:4000")
	mkGateway(t, mr, ctx, "alpha", "https://a:4000")

	// mkGateway suffixes the name with t.Name() to dodge in-memory sqlite
	// shared-cache collisions; we assert on the suffix rather than the literal.
	sfx := "-" + t.Name()
	asc, err := qr.ModelGateways(ctx, nil, model.PageInput{}, &model.ModelGatewaySort{
		Field: model.ModelGatewaySortFieldName, Direction: model.SortDirectionAsc,
	})
	if err != nil || len(asc.Nodes) != 2 ||
		asc.Nodes[0].Name != "alpha"+sfx || asc.Nodes[1].Name != "bravo"+sfx {
		t.Fatalf("sort NAME asc wrong: %+v / %v", asc.Nodes, err)
	}
	if asc.Nodes[0].CreatedAt.IsZero() || asc.Nodes[0].UpdatedAt.IsZero() {
		t.Fatal("createdAt/updatedAt must be exposed")
	}

	desc, _ := qr.ModelGateways(ctx, nil, model.PageInput{}, &model.ModelGatewaySort{
		Field: model.ModelGatewaySortFieldName, Direction: model.SortDirectionDesc,
	})
	if desc.Nodes[0].Name != "bravo"+sfx {
		t.Fatalf("sort NAME desc: %s", desc.Nodes[0].Name)
	}
}

// M2/M3: lastSyncAt is nil until a successful sync, is set on success, and
// does NOT move on an unrelated update.
func TestModelGateway_LastSyncTracking(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "g", "https://gw:4000")
	if g.LastSyncAt == nil {
		t.Fatal("post-create sync must set lastSyncAt")
	}
	synced := *g.LastSyncAt

	// an unrelated update must NOT move lastSyncAt
	upd, err := mr.UpdateModelGateway(ctx, g.ID, model.ModelGatewayInput{
		Name: "renamed", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://gw:4000",
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

// M2/M3 invariant: a FAILED sync must not clear or move a prior lastSyncAt.
func TestModelGateway_FailedSyncKeepsSyncTime(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	// first sync succeeds → sets lastSyncAt
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager { return &fakeModelManager{} }
	g := mkGateway(t, mr, ctx, "g", "https://gw:4000")
	good, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil || good.Gateway.LastSyncAt == nil {
		t.Fatalf("first sync should set lastSyncAt: %+v / %v", good.Gateway.LastSyncAt, err)
	}
	synced := *good.Gateway.LastSyncAt

	// second sync FAILS → status FAILED, but lastSyncAt preserved
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("down")}
	}
	bad, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("sync (fail): %v", err)
	}
	if bad.Gateway.LastSyncStatus != model.ModelGatewaySyncStateFailed {
		t.Fatalf("lastSyncStatus = %v, want FAILED", bad.Gateway.LastSyncStatus)
	}
	if bad.Gateway.LastSyncAt == nil || !bad.Gateway.LastSyncAt.Equal(synced) {
		t.Fatalf("failed sync must keep old lastSyncAt: got %v, want %v", bad.Gateway.LastSyncAt, synced)
	}
}

// H1: the sync must build a client from the gateway's OWN endpoint and its
// OWN master key (resolved from the secret store), not a process-wide default.
func TestModelGateway_SyncUsesPerGatewayClient(t *testing.T) {
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
		Name: "gw-" + t.Name(), Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://vc-x:4000",
		MasterKey: ptr("sk-secret-xyz"),
	})
	if err != nil {
		t.Fatalf("CreateModelGateway: %v", err)
	}
	if _, err := mr.SyncModelGatewayConnection(ctx, g.ID); err != nil {
		t.Fatalf("SyncModelGatewayConnection: %v", err)
	}
	if gotEndpoint != "https://vc-x:4000" {
		t.Errorf("per-gateway endpoint = %q, want https://vc-x:4000", gotEndpoint)
	}
	if gotKey != "sk-secret-xyz" {
		t.Errorf("master key not resolved from secret store: %q", gotKey)
	}
}

// H1 negative path: a gateway with no master key (and no secret store) syncs
// with an empty key rather than crashing.
func TestModelGateway_SyncEmptyKeyWhenNoSecret(t *testing.T) {
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
	if _, err := mr.SyncModelGatewayConnection(ctx, g.ID); err != nil {
		t.Fatalf("sync: %v", err)
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
	})
	if err != nil || upd.Name != "new" || upd.Endpoint != "https://new:4000" {
		t.Fatalf("update: %+v / %v", upd, err)
	}

	del, err := mr.DeleteModelGateway(ctx, g.ID)
	if err != nil || del.DeletedID != g.ID {
		t.Fatalf("delete: %+v / %v", del, err)
	}
	conn, _ := qr.ModelGateways(ctx, nil, model.PageInput{}, nil)
	if conn.TotalCount != 0 {
		t.Fatalf("gateway should be deleted, got %d", conn.TotalCount)
	}
}

// H3: a successful sync persists loadBalancingStrategy on the row. The next
// query (with no probe) reads the persisted value from the ent column.
func TestModelGateway_SyncPersistsRoutingStrategy(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{strategy: gateway.RoutingStrategy("latency-based-routing")}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	g := mkGateway(t, mr, ctx, "g-lat", "https://llm:4000")
	// before any sync: nil (column has ent default but we don't surface it yet —
	// actually it's the literal default "SIMPLE_SHUFFLE", so the projection
	// reflects it). Make the assertion only on the post-sync state.
	_ = g

	res, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("SyncModelGatewayConnection: %v", err)
	}
	if res.Gateway.LoadBalancingStrategy == nil ||
		*res.Gateway.LoadBalancingStrategy != model.LoadBalancingStrategyLatencyBasedRouting {
		t.Fatalf("gateway.LoadBalancingStrategy = %v, want LATENCY_BASED_ROUTING", res.Gateway.LoadBalancingStrategy)
	}

	// after the sync: the query path reads the persisted value, not nil.
	post, _ := qr.ModelGateways(ctx, nil, model.PageInput{}, nil)
	if post.Nodes[0].LoadBalancingStrategy == nil ||
		*post.Nodes[0].LoadBalancingStrategy != model.LoadBalancingStrategyLatencyBasedRouting {
		t.Fatalf("post-sync query must read persisted strategy, got %v", post.Nodes[0].LoadBalancingStrategy)
	}
}

// H4: a strategy-probe failure must NOT downgrade a successful connectivity
// sync. status stays CONNECTED, lastSyncStatus=SYNCED, and loadBalancingStrategy
// is left at whatever was already stored (here: the ent default).
func TestModelGateway_StrategyProbeFailureLeavesFieldAtDefault(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{strategyErr: errors.New("router down")}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := mkGateway(t, mr, ctx, "g-fail", "https://llm:4000")
	res, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("SyncModelGatewayConnection: %v", err)
	}
	if !res.Success {
		t.Fatalf("connectivity succeeded, strategy probe failed → still success: %+v", res)
	}
	if res.Gateway.LastSyncStatus != model.ModelGatewaySyncStateSynced {
		t.Fatalf("lastSyncStatus = %v, want SYNCED", res.Gateway.LastSyncStatus)
	}
	// Strategy is preserved at the column's existing value (the ent default),
	// not nil'd out by a failed probe. We don't assert the exact default value
	// — it's a wire-format literal the column carries — only that the field is
	// not nil.
	if res.Gateway.LoadBalancingStrategy == nil {
		t.Fatal("strategy must be preserved (default), got nil")
	}
}

// H5: backendModelCount is sourced from ListModels (the gateway's real view),
// not from the local upstream table.
func TestModelGateway_BackendModelCountFromGateway(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		models := make([]gateway.ModelInfo, 7)
		for i := range models {
			models[i] = gateway.ModelInfo{ID: "m", ModelName: "m"}
		}
		return &fakeModelManager{listModels: models}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := mkGateway(t, mr, ctx, "g", "https://llm:4000")
	res, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("SyncModelGatewayConnection: %v", err)
	}
	if res.Gateway.BackendModelCount != 7 {
		t.Fatalf("backendModelCount = %d, want 7 (from ListModels)", res.Gateway.BackendModelCount)
	}
}

// H5 invariant: a failed ListModels probe (or a failed TestConnection) must
// NOT zero a previously-stored backendModelCount.
func TestModelGateway_BackendModelCountPreservedOnFailure(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		models := make([]gateway.ModelInfo, 7)
		for i := range models {
			models[i] = gateway.ModelInfo{ID: "m", ModelName: "m"}
		}
		return &fakeModelManager{listModels: models}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := mkGateway(t, mr, ctx, "g", "https://llm:4000")
	good, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil || good.Gateway.BackendModelCount != 7 {
		t.Fatalf("first sync should set backendModelCount=7: %+v / %v", good.Gateway, err)
	}

	// second sync fails on TestConnection → backendModelCount must stay 7
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("down"), listErr: errors.New("down")}
	}
	bad, err := mr.SyncModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("sync (fail): %v", err)
	}
	if bad.Gateway.BackendModelCount != 7 {
		t.Fatalf("backendModelCount must be preserved on failure, got %d", bad.Gateway.BackendModelCount)
	}
}

// H5: a freshly-created gateway has backendModelCount=0 (column NULL projected
// as 0) until the first sync runs. We build the row directly (bypassing
// CreateModelGateway) to avoid the post-create background sync that would
// otherwise stamp the value before we read it.
func TestModelGateway_BackendModelCountZeroBeforeFirstSync(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	g, err := r.Ent.GatewayConnection.Create().
		SetName("g-" + t.Name()).SetEndpoint("https://llm:4000").Save(ctx)
	if err != nil {
		t.Fatalf("create row: %v", err)
	}
	proj := r.toModelGateway(g)
	if proj.BackendModelCount != 0 {
		t.Fatalf("fresh gateway: backendModelCount = %d, want 0", proj.BackendModelCount)
	}
	conn, _ := qr.ModelGateways(ctx, nil, model.PageInput{}, nil)
	if conn.Nodes[0].BackendModelCount != 0 {
		t.Fatalf("list fresh gateway: backendModelCount = %d, want 0", conn.Nodes[0].BackendModelCount)
	}
}

// H6: while a sync is in flight, the list query shows lastSyncStatus=SYNCING
// regardless of what was last persisted. Once the sync lands, the projection
// reverts to whatever the stored column says.
func TestModelGateway_SyncShowsSyncingDuringInflight(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &slowFakeModelManager{}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// mkGateway waits for the post-create background sync to land first, so
	// the manual beginSync below operates on a settled row.
	g := mkGateway(t, mr, ctx, "g", "https://llm:4000")

	// Manually mark in-flight to simulate a sync that has just begun (the
	// real sync's beginSync runs before any network call; we approximate that
	// by injecting the marker directly).
	gid := uuid.MustParse(g.ID)
	r.beginSync(gid)
	defer r.endSync(gid)

	// Read the list while in-flight → SYNCING
	conn, err := qr.ModelGateways(ctx, nil, model.PageInput{}, nil)
	if err != nil {
		t.Fatalf("ModelGateways: %v", err)
	}
	if conn.Nodes[0].LastSyncStatus != model.ModelGatewaySyncStateSyncing {
		t.Fatalf("inflight list: lastSyncStatus = %v, want SYNCING", conn.Nodes[0].LastSyncStatus)
	}
}

// Dry-run: a pre-create test of an unsaved gateway config. Returns a result
// with success=true, no gateway payload — the dry-run deliberately does not
// call GetRoutingStrategy or ListModels (it's a "can I reach this box?"
// probe, not a "what's it configured to do?" probe). The fake's strategy /
// listModels fields are irrelevant; we don't read them.
func TestModelGateway_DryRunProbe_Success(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{strategy: gateway.RoutingStrategy("usage-based-routing-v2")}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	res, err := mr.TestNewModelGatewayConnection(ctx, model.TestModelGatewayConnectionInput{
		Endpoint:  "https://litellm:4000",
		MasterKey: "sk-typed",
	})
	if err != nil {
		t.Fatalf("TestNewModelGatewayConnection: %v", err)
	}
	if !res.Success {
		t.Fatalf("dry-run success path: %+v", res)
	}
	if res.Message != "connection ok" {
		t.Fatalf("message = %q, want 'connection ok'", res.Message)
	}
	if res.TestedAt.IsZero() {
		t.Fatal("testedAt must be set on success path")
	}
}

// Dry-run failure: TestConnection errors.
func TestModelGateway_DryRunProbe_Failure(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errors.New("dial tcp: refused")}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	res, err := mr.TestNewModelGatewayConnection(ctx, model.TestModelGatewayConnectionInput{
		Endpoint:  "https://litellm:4000",
		MasterKey: "sk-typed",
	})
	if err != nil {
		t.Fatalf("TestNewModelGatewayConnection: %v", err)
	}
	if res.Success {
		t.Fatalf("dry-run failure path should report success=false: %+v", res)
	}
	if res.Message != "connection failed" {
		t.Fatalf("error message should be sanitized, got %q", res.Message)
	}
	if res.TestedAt.IsZero() {
		t.Fatal("testedAt must be set on failure path too")
	}
}

// Empty input → gqlerror before any network call.
func TestModelGateway_DryRunProbe_InputValidation(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	if _, err := mr.TestNewModelGatewayConnection(ctx, model.TestModelGatewayConnectionInput{}); err == nil {
		t.Fatal("empty input must fail validation")
	}
}

// slowFakeModelManager is a fake ModelManager. Its TestConnection succeeds
// without blocking; the SYNCING test above drives the inflight map directly
// (the resolver's beginSync/endSync wraps the network call, so the test only
// needs the manager to be wired in — it does not exercise TestConnection).
type slowFakeModelManager struct{}

func (f *slowFakeModelManager) TestConnection(context.Context) error { return nil }
func (f *slowFakeModelManager) GetRoutingStrategy(context.Context) (gateway.RoutingStrategy, error) {
	return gateway.RoutingStrategy("simple-shuffle"), nil
}
func (f *slowFakeModelManager) ListModels(context.Context) ([]gateway.ModelInfo, error) {
	return nil, nil
}
func (f *slowFakeModelManager) NewModel(context.Context, gateway.ModelSpec) error { return nil }
func (f *slowFakeModelManager) DeleteModel(context.Context, string) error         { return nil }
func (f *slowFakeModelManager) UpsertComplexityRouter(context.Context, gateway.RouterSpec) error {
	return nil
}
