package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// Edge-case coverage for the model-gateway and model-route resolvers. These
// complement modelgateway_test.go / gateway_routing_test.go by exercising the
// empty-state, not-found, and invalid-id paths plus the setModelRouteEnabled
// toggle and the testModelGatewayConnection result shape. Helper/var names carry
// a unique `_edge` suffix to avoid collisions with sibling test files in this
// package.

// nonexistentUUIDEdge is a well-formed UUID that is guaranteed not to be present
// in a fresh in-memory store, so resolvers must hit their not-found branch.
const nonexistentUUIDEdge = "ffffffff-ffff-ffff-ffff-ffffffffffff"

// mkRouteEdge creates a model route via CreateModelRoute and returns it.
func mkRouteEdge(t *testing.T, mr *mutationResolver, ctx context.Context, name string, models []string) *model.ModelRoute {
	t.Helper()
	rt, err := mr.CreateModelRoute(ctx, model.CreateModelRouteInput{
		Name:            name,
		SupportedModels: models,
	})
	if err != nil {
		t.Fatalf("CreateModelRoute(%s): %v", name, err)
	}
	return rt
}

// ---- empty-state ----

// On a fresh store, the list queries must return empty (non-nil) collections and
// zero totals — not nil, not errors.
func TestModelGateways_EmptyStateEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	conn, err := qr.ModelGateways(ctx, nil, model.PageInput{}, nil)
	if err != nil {
		t.Fatalf("ModelGateways (empty): %v", err)
	}
	if conn == nil || conn.TotalCount != 0 {
		t.Fatalf("empty gateways: totalCount = %v, want 0", conn)
	}
	if conn.Nodes == nil {
		t.Fatal("empty gateways: Nodes must be a non-nil slice")
	}
	if len(conn.Nodes) != 0 {
		t.Fatalf("empty gateways: %d nodes, want 0", len(conn.Nodes))
	}

	// The sync summary over an empty fleet is NEVER with zero counts and no time.
	sum, err := qr.ModelGatewaySyncSummary(ctx)
	if err != nil {
		t.Fatalf("ModelGatewaySyncSummary (empty): %v", err)
	}
	if sum.State != model.ModelGatewaySyncStateNever || sum.SuccessCount != 0 ||
		sum.FailedCount != 0 || sum.LastSyncedAt != nil {
		t.Fatalf("empty sync summary: %+v", sum)
	}
}

func TestModelRoutes_EmptyStateEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	qr := &queryResolver{r}

	routes, err := qr.ModelRoutes(ctx)
	if err != nil {
		t.Fatalf("ModelRoutes (empty): %v", err)
	}
	if routes == nil {
		t.Fatal("empty routes: must be a non-nil slice")
	}
	if len(routes) != 0 {
		t.Fatalf("empty routes: %d, want 0", len(routes))
	}
}

// ---- not-found (well-formed id, absent row) ----

// Mutations that fetch a row first must surface a not-found error (not a panic,
// not a nil result) when given a valid-but-absent id.
func TestModelGateway_NotFoundEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	// testModelGatewayConnection Get()s the row → not-found error, never a panic.
	res, err := mr.TestModelGatewayConnection(ctx, nonexistentUUIDEdge)
	if err == nil {
		t.Fatalf("TestModelGatewayConnection(absent): expected error, got result %+v", res)
	}
	if res != nil {
		t.Fatalf("TestModelGatewayConnection(absent): result must be nil on error, got %+v", res)
	}

	// updateOneID on an absent row also errors (no row to update).
	if _, err := mr.UpdateModelGateway(ctx, nonexistentUUIDEdge, model.ModelGatewayInput{
		Name: "x", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://x:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	}); err == nil {
		t.Fatal("UpdateModelGateway(absent): expected not-found error")
	}

	// deleteOneID on an absent row also errors.
	if _, err := mr.DeleteModelGateway(ctx, nonexistentUUIDEdge); err == nil {
		t.Fatal("DeleteModelGateway(absent): expected not-found error")
	}
}

func TestModelRoute_NotFoundEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	if _, err := mr.UpdateModelRoute(ctx, nonexistentUUIDEdge, model.UpdateModelRouteInput{
		Name: ptr("x"),
	}); err == nil {
		t.Fatal("UpdateModelRoute(absent): expected not-found error")
	}
	if _, err := mr.SetModelRouteEnabled(ctx, nonexistentUUIDEdge, false); err == nil {
		t.Fatal("SetModelRouteEnabled(absent): expected not-found error")
	}
	if ok, err := mr.DeleteModelRoute(ctx, nonexistentUUIDEdge); err == nil || ok {
		t.Fatalf("DeleteModelRoute(absent): want (false, err), got (%v, %v)", ok, err)
	}
}

// ---- invalid id (malformed, not a UUID) ----

// Every id-taking mutation must reject a malformed id with the "invalid id"
// gqlerror BEFORE touching the store, and must not panic.
func TestModelGateway_InvalidIDEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	if _, err := mr.UpdateModelGateway(ctx, "not-a-uuid", model.ModelGatewayInput{
		Name: "x", Provider: model.ModelGatewayProviderLitellm, Endpoint: "https://x:4000",
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
	}); err == nil || err.Error() != "input: invalid id" {
		t.Fatalf("UpdateModelGateway(bad id): err = %v, want \"input: invalid id\"", err)
	}
	if _, err := mr.DeleteModelGateway(ctx, "not-a-uuid"); err == nil || err.Error() != "input: invalid id" {
		t.Fatalf("DeleteModelGateway(bad id): err = %v", err)
	}
	if res, err := mr.TestModelGatewayConnection(ctx, "not-a-uuid"); err == nil || res != nil {
		t.Fatalf("TestModelGatewayConnection(bad id): res=%v err=%v, want nil/err", res, err)
	}
}

func TestModelRoute_InvalidIDEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	if _, err := mr.UpdateModelRoute(ctx, "bad", model.UpdateModelRouteInput{Name: ptr("x")}); err == nil ||
		err.Error() != "input: invalid id" {
		t.Fatalf("UpdateModelRoute(bad id): err = %v", err)
	}
	if _, err := mr.SetModelRouteEnabled(ctx, "bad", true); err == nil || err.Error() != "input: invalid id" {
		t.Fatalf("SetModelRouteEnabled(bad id): err = %v", err)
	}
	if ok, err := mr.DeleteModelRoute(ctx, "bad"); err == nil || ok {
		t.Fatalf("DeleteModelRoute(bad id): ok=%v err=%v", ok, err)
	}

	// CreateModelRoute / UpdateModelRoute reject a malformed backendGatewayId with a
	// field-named error (not the generic "invalid id").
	if _, err := mr.CreateModelRoute(ctx, model.CreateModelRouteInput{
		Name: "r", BackendGatewayID: ptr("not-a-uuid"),
	}); err == nil || err.Error() != "input: invalid backendGatewayId" {
		t.Fatalf("CreateModelRoute(bad gw id): err = %v", err)
	}
}

// ---- create / update / delete happy paths ----

// CreateModelRoute always inserts (id-keyed), defaults model_alias to the route
// name, stores supportedModels as the upstream group, and honors enabled/uiStrategy.
func TestModelRoute_CreateHappyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	rt, err := mr.CreateModelRoute(ctx, model.CreateModelRouteInput{
		Name:            "smart-route",
		SupportedModels: []string{"gpt-4", "claude"},
		UIStrategy:      ptr(model.ModelRouteStrategyWeightedRoundRobin),
		Enabled:         ptr(false),
	})
	if err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}
	if _, perr := uuid.Parse(rt.ID); perr != nil {
		t.Fatalf("route id not a uuid: %q", rt.ID)
	}
	if rt.Name != "smart-route" || rt.ModelAlias != "smart-route" {
		t.Fatalf("name/alias wrong: %+v", rt)
	}
	if len(rt.SupportedModels) != 2 || rt.SupportedModels[0] != "gpt-4" {
		t.Fatalf("supportedModels not stored: %+v", rt.SupportedModels)
	}
	// SupportedModels and Upstreams are the same backing group.
	if len(rt.Upstreams) != 2 || rt.Upstreams[1] != "claude" {
		t.Fatalf("upstreams group wrong: %+v", rt.Upstreams)
	}
	if rt.UIStrategy != model.ModelRouteStrategyWeightedRoundRobin {
		t.Fatalf("uiStrategy = %v", rt.UIStrategy)
	}
	if rt.Enabled {
		t.Fatal("enabled=false must be honored")
	}

	routes, _ := qr.ModelRoutes(ctx)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route after create, got %d", len(routes))
	}
}

// UpdateModelRoute applies only provided fields; supportedModels (when given)
// replaces the upstream group; omitted fields are left untouched.
func TestModelRoute_UpdateHappyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	rt := mkRouteEdge(t, mr, ctx, "orig", []string{"m1"})

	upd, err := mr.UpdateModelRoute(ctx, rt.ID, model.UpdateModelRouteInput{
		Name:            ptr("renamed"),
		SupportedModels: []string{"m2", "m3"},
		UIStrategy:      ptr(model.ModelRouteStrategyRandom),
	})
	if err != nil {
		t.Fatalf("UpdateModelRoute: %v", err)
	}
	if upd.ID != rt.ID {
		t.Fatalf("update must keep id: got %s want %s", upd.ID, rt.ID)
	}
	if upd.Name != "renamed" {
		t.Fatalf("name not updated: %q", upd.Name)
	}
	if len(upd.SupportedModels) != 2 || upd.SupportedModels[0] != "m2" {
		t.Fatalf("supportedModels not replaced: %+v", upd.SupportedModels)
	}
	if upd.UIStrategy != model.ModelRouteStrategyRandom {
		t.Fatalf("uiStrategy not updated: %v", upd.UIStrategy)
	}
}

// DeleteModelRoute happy path removes the row; the list then no longer returns it.
func TestModelRoute_DeleteHappyEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	a := mkRouteEdge(t, mr, ctx, "keep", nil)
	b := mkRouteEdge(t, mr, ctx, "drop", nil)

	ok, err := mr.DeleteModelRoute(ctx, b.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteModelRoute: ok=%v err=%v", ok, err)
	}
	routes, _ := qr.ModelRoutes(ctx)
	if len(routes) != 1 || routes[0].ID != a.ID {
		t.Fatalf("only the kept route should remain: %+v", routes)
	}
}

// ---- setModelRouteEnabled toggle ----

// SetModelRouteEnabled flips the persisted flag both directions and returns the
// updated route; the change is durable (visible on a subsequent list).
func TestSetModelRouteEnabled_TogglesEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	rt := mkRouteEdge(t, mr, ctx, "toggle", nil)
	if !rt.Enabled {
		t.Fatalf("route should default to enabled, got %+v", rt)
	}

	off, err := mr.SetModelRouteEnabled(ctx, rt.ID, false)
	if err != nil {
		t.Fatalf("SetModelRouteEnabled(false): %v", err)
	}
	if off.ID != rt.ID || off.Enabled {
		t.Fatalf("expected disabled route, got %+v", off)
	}

	// durable across a re-read
	routes, _ := qr.ModelRoutes(ctx)
	if len(routes) != 1 || routes[0].Enabled {
		t.Fatalf("disable not persisted: %+v", routes)
	}

	on, err := mr.SetModelRouteEnabled(ctx, rt.ID, true)
	if err != nil || !on.Enabled {
		t.Fatalf("SetModelRouteEnabled(true): %+v / %v", on, err)
	}
}

// ---- testModelGatewayConnection result shape ----

// A successful connection test returns a fully-populated result: success=true,
// CONNECTED status, a non-nil latency, the sanitized "connection ok" message, a
// set TestedAt timestamp, and the embedded gateway flipped to connected.
func TestModelGatewayConnection_ResultShapeEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{} // TestConnection → nil → connected
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := mkGateway(t, mr, ctx, "shape-gw", "https://gw:4000")
	res, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("TestModelGatewayConnection: %v", err)
	}
	if !res.Success {
		t.Fatalf("success = false, want true: %+v", res)
	}
	if res.Status != model.ModelGatewayStatusConnected {
		t.Fatalf("status = %v, want CONNECTED", res.Status)
	}
	if res.LatencyMs == nil || *res.LatencyMs < 0 {
		t.Fatalf("latencyMs must be a non-negative int, got %v", res.LatencyMs)
	}
	if res.Message != "connection ok" {
		t.Fatalf("success message = %q, want \"connection ok\"", res.Message)
	}
	if res.TestedAt.IsZero() {
		t.Fatal("testedAt must be set")
	}
	if res.Gateway == nil || res.Gateway.ID != g.ID {
		t.Fatalf("embedded gateway must echo the tested row: %+v", res.Gateway)
	}
	if res.Gateway.Status != model.ModelGatewayStatusConnected {
		t.Fatalf("embedded gateway status = %v, want CONNECTED", res.Gateway.Status)
	}
}

// On a failed test the result shape stays well-formed: success=false, ERROR
// status, and a sanitized message that does NOT leak the raw transport error
// (which here embeds an endpoint-looking string).
func TestModelGatewayConnection_FailedShapeEdge(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.GatewayClientFor = func(context.Context, string, string) gateway.ModelManager {
		return &fakeModelManager{testErr: errEdgeConnRefused}
	}
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := mkGateway(t, mr, ctx, "fail-gw", "https://secret-host:4000")
	res, err := mr.TestModelGatewayConnection(ctx, g.ID)
	if err != nil {
		t.Fatalf("TestModelGatewayConnection (fail path): %v", err)
	}
	if res.Success {
		t.Fatal("success must be false on a failed test")
	}
	if res.Status != model.ModelGatewayStatusError {
		t.Fatalf("status = %v, want ERROR", res.Status)
	}
	if res.Message != "connection failed" {
		t.Fatalf("error message must be sanitized to \"connection failed\", got %q", res.Message)
	}
	if res.Gateway == nil || res.Gateway.Status != model.ModelGatewayStatusError {
		t.Fatalf("embedded gateway must reflect ERROR: %+v", res.Gateway)
	}
}

// errEdgeConnRefused is a transport error whose text resembles an endpoint, used
// to prove the failed-test message never echoes the raw error.
var errEdgeConnRefused = &edgeErr{"dial tcp secret-host:4000: connect: connection refused"}

type edgeErr struct{ s string }

func (e *edgeErr) Error() string { return e.s }
