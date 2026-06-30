package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// TestGatewayKeyClientForVK_RoutesToIssuingGatewayAfterRebind is the core bug #5
// regression (LLD-14 T2): a key's lifecycle routes to the gateway that ISSUED it
// (its persisted gateway_connection_id), NOT the department's *current* binding —
// so re-binding the department's gateway can't strand the key as an active orphan.
func TestGatewayKeyClientForVK_RoutesToIssuingGatewayAfterRebind(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()

	var routedTo string
	r.GatewayKeyClientFor = func(_ context.Context, g *ent.GatewayConnection) gateway.Client {
		routedTo = g.Endpoint
		return &fakeGateway{}
	}

	gwA := r.Ent.GatewayConnection.Create().SetName("A").SetEndpoint("https://A").SetIsDefault(true).SaveX(ctx)
	gwB := r.Ent.GatewayConnection.Create().SetName("B").SetEndpoint("https://B").SaveX(ctx)
	// Department NOW bound to gw-B — simulating a re-bind AFTER the key below was
	// issued on gw-A.
	dept := r.Ent.Department.Create().SetName("d").SetGatewayConnectionID(gwB.ID).SaveX(ctx)

	// A key issued on gw-A (gateway_connection_id = A) whose team_id points at the dept.
	vk := r.Ent.VirtualKey.Create().
		SetLitellmKey("k").SetUserID(uuid.New()).
		SetTeamID(dept.ID.String()).SetGatewayConnectionID(gwA.ID).SaveX(ctx)

	routedTo = ""
	r.gatewayKeyClientForVK(ctx, vk)
	if routedTo != "https://A" {
		t.Fatalf("key lifecycle must route to the issuing gateway A, got %q (dept now points at B — bug #5 regression)", routedTo)
	}

	// A legacy key (NULL gateway_connection_id, minted before T1) falls back to the
	// team→department→gateway derivation = the dept's CURRENT gateway (B).
	vkLegacy := r.Ent.VirtualKey.Create().
		SetLitellmKey("k2").SetUserID(uuid.New()).SetTeamID(dept.ID.String()).SaveX(ctx)
	routedTo = ""
	r.gatewayKeyClientForVK(ctx, vkLegacy)
	if routedTo != "https://B" {
		t.Fatalf("legacy NULL key must fall back to the dept gateway B, got %q", routedTo)
	}
}

// TestDeleteModelGateway_RefusedWhenActiveKeyReferences pins the LLD-14 §3.5 delete
// guard: a gateway with a non-revoked key minted on it can't be deleted (that key
// could only be revoked there), but becomes deletable once the key is revoked.
func TestDeleteModelGateway_RefusedWhenActiveKeyReferences(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}

	g := r.Ent.GatewayConnection.Create().SetName("gw").SetEndpoint("http://x:4000").SaveX(ctx)
	vk := r.Ent.VirtualKey.Create().
		SetLitellmKey("k").SetUserID(uuid.New()).SetGatewayConnectionID(g.ID).SaveX(ctx)

	if _, err := mr.DeleteModelGateway(ctx, g.ID.String()); err == nil {
		t.Fatal("must refuse to delete a gateway an active key references")
	}

	// Revoke the key → the gateway is now deletable.
	r.Ent.VirtualKey.UpdateOne(vk).SetStatus(virtualkey.StatusRevoked).SaveX(ctx)
	if _, err := mr.DeleteModelGateway(ctx, g.ID.String()); err != nil {
		t.Fatalf("must delete a gateway once its keys are revoked: %v", err)
	}
}

// TestRevokeVirtualKey_RoutesToIssuingGatewayAfterDefaultSwap pins the actually
// reachable bug #5 vector end-to-end (through the RevokeVirtualKey resolver, not
// just the helper): a no-department key records the platform default gateway at
// issue (T1); swapping the platform default to another gateway must NOT reroute the
// key's revoke — it still hits the gateway that issued it (T2).
func TestRevokeVirtualKey_RoutesToIssuingGatewayAfterDefaultSwap(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	injectFakeGatewayModels(r)

	var routedTo string
	r.GatewayKeyClientFor = func(_ context.Context, g *ent.GatewayConnection) gateway.Client {
		routedTo = g.Endpoint
		return &fakeGateway{}
	}
	mr := &mutationResolver{r}
	yes := true

	// gw-A is the platform default; a no-department key is issued on it → records A.
	gwA, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "A", Endpoint: "https://A", IsDefault: &yes,
	})
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{UserID: uuid.New().String()})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	vk := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued.VirtualKey.ID))
	if vk.GatewayConnectionID == nil || vk.GatewayConnectionID.String() != gwA.ID {
		t.Fatalf("(T1) key should record issuing gw-A, got %v", vk.GatewayConnectionID)
	}

	// Swap the platform default to gw-B.
	if _, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "B", Endpoint: "https://B", IsDefault: &yes,
	}); err != nil {
		t.Fatalf("register B as new default: %v", err)
	}

	// Revoke must hit the ISSUING gateway A (recorded), not the new default B.
	routedTo = ""
	if ok, err := mr.RevokeVirtualKey(ctx, issued.VirtualKey.ID); err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	if routedTo != gwA.Endpoint {
		t.Fatalf("revoke must route to the issuing gateway A, got %q (default is now B — bug #5)", routedTo)
	}
}
