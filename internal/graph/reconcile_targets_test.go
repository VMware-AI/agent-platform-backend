package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// taggedGW is a gateway.Client whose identity (the connection endpoint) can be
// read back from a reconcile target, so a partition test can assert which gateway
// each key/department was assigned to.
type taggedGW struct {
	fakeGateway
	endpoint string
}

// keysOf returns the litellm_key of every VirtualKey row a target carries.
func keysOf(rows []*ent.VirtualKey) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, vk := range rows {
		out[vk.LitellmKey] = true
	}
	return out
}

func deptNamesOf(rows []*ent.Department) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, d := range rows {
		out[d.Name] = true
	}
	return out
}

// TestReconcileTargets_PartitionsRowsAcrossGateways is the LLD-14 §3.4 / OQ-5
// partition: every key is assigned to the gateway that issued it (persisted
// gateway_connection_id); a legacy NULL key falls back to its team→department's
// CURRENT gateway, else the platform default; departments follow binding-else-
// default. Each gateway then reconciles only its own slice.
func TestReconcileTargets_PartitionsRowsAcrossGateways(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	r.GatewayKeyClientFor = func(_ context.Context, g *ent.GatewayConnection) gateway.Client {
		return &taggedGW{endpoint: g.Endpoint}
	}

	gwA := r.Ent.GatewayConnection.Create().SetName("A").SetEndpoint("https://A").SetIsDefault(true).SaveX(ctx)
	gwB := r.Ent.GatewayConnection.Create().SetName("B").SetEndpoint("https://B").SaveX(ctx)

	deptA := r.Ent.Department.Create().SetName("deptA").SetGatewayConnectionID(gwA.ID).SaveX(ctx)
	deptB := r.Ent.Department.Create().SetName("deptB").SetGatewayConnectionID(gwB.ID).SaveX(ctx)

	mk := func(key string, conn *uuid.UUID, team string) {
		b := r.Ent.VirtualKey.Create().SetLitellmKey(key).SetUserID(uuid.New())
		if conn != nil {
			b.SetGatewayConnectionID(*conn)
		}
		if team != "" {
			b.SetTeamID(team)
		}
		b.SaveX(ctx)
	}
	mk("kOnA", &gwA.ID, "")                  // issued on A
	mk("kOnB", &gwB.ID, "")                  // issued on B
	mk("kNullTeamB", nil, deptB.ID.String()) // legacy NULL, team→deptB (bound to B) ⇒ B
	mk("kNullTeamA", nil, deptA.ID.String()) // legacy NULL, team→deptA (bound to A) ⇒ A
	mk("kNullNoTeam", nil, "")               // legacy NULL, no team ⇒ default (A)

	targets, err := r.ReconcileTargets(ctx)
	if err != nil {
		t.Fatalf("ReconcileTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("want 2 gateway targets, got %d", len(targets))
	}

	byEndpoint := map[string]reconcileTargetView{}
	for _, tg := range targets {
		g, ok := tg.Gateway.(*taggedGW)
		if !ok {
			t.Fatalf("target gateway is not a taggedGW: %T", tg.Gateway)
		}
		byEndpoint[g.endpoint] = reconcileTargetView{keys: keysOf(tg.Keys), depts: deptNamesOf(tg.Depts)}
	}

	a, ok := byEndpoint["https://A"]
	if !ok {
		t.Fatal("no target for gateway A")
	}
	b, ok := byEndpoint["https://B"]
	if !ok {
		t.Fatal("no target for gateway B")
	}

	// Gateway A owns its issued key + both default-routed legacy keys.
	for _, want := range []string{"kOnA", "kNullTeamA", "kNullNoTeam"} {
		if !a.keys[want] {
			t.Errorf("gateway A missing key %q (keys=%v)", want, a.keys)
		}
	}
	if a.keys["kOnB"] || a.keys["kNullTeamB"] {
		t.Errorf("gateway A wrongly owns a B key (keys=%v)", a.keys)
	}
	// Gateway B owns its issued key + the legacy key whose dept binds to B.
	for _, want := range []string{"kOnB", "kNullTeamB"} {
		if !b.keys[want] {
			t.Errorf("gateway B missing key %q (keys=%v)", want, b.keys)
		}
	}
	if len(b.keys) != 2 {
		t.Errorf("gateway B should own exactly 2 keys, got %v", b.keys)
	}
	// Departments follow their binding.
	if !a.depts["deptA"] || a.depts["deptB"] {
		t.Errorf("gateway A departments wrong: %v", a.depts)
	}
	if !b.depts["deptB"] || b.depts["deptA"] {
		t.Errorf("gateway B departments wrong: %v", b.depts)
	}
}

type reconcileTargetView struct {
	keys  map[string]bool
	depts map[string]bool
}

// TestReconcileTargets_LegacySingleGatewayFallback: with no GatewayConnection rows,
// the injected r.Gateway reconciles all rows as one target (pre-LLD-13 install).
func TestReconcileTargets_LegacySingleGatewayFallback(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	legacy := &fakeGateway{}
	r.Gateway = legacy

	r.Ent.VirtualKey.Create().SetLitellmKey("k1").SetUserID(uuid.New()).SaveX(ctx)
	r.Ent.Department.Create().SetName("d1").SaveX(ctx)

	targets, err := r.ReconcileTargets(ctx)
	if err != nil {
		t.Fatalf("ReconcileTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1 legacy target, got %d", len(targets))
	}
	if targets[0].Gateway != legacy {
		t.Errorf("legacy target should reuse the injected r.Gateway")
	}
	if len(targets[0].Keys) != 1 || len(targets[0].Depts) != 1 {
		t.Errorf("legacy target should hold all rows: keys=%d depts=%d", len(targets[0].Keys), len(targets[0].Depts))
	}
}

// TestReconcileTargets_NoGatewayAtAll returns nil (caller skips the cycle).
func TestReconcileTargets_NoGatewayAtAll(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	targets, err := r.ReconcileTargets(context.Background())
	if err != nil {
		t.Fatalf("ReconcileTargets: %v", err)
	}
	if targets != nil {
		t.Errorf("want nil targets when no gateway configured, got %v", targets)
	}
}
