package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// healthReader adds a scripted Health to the fakeSpendReader contract.
type healthReader struct {
	fakeSpendReader
	health *gateway.GatewayHealth
	err    error
}

func (h healthReader) Health(context.Context) (*gateway.GatewayHealth, error) {
	return h.health, h.err
}

func TestGatewayHealth_FanOutAndDegrade(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := r.Ent.GatewayConnection.Create().SetName("gw-a").SetEndpoint("http://a:4000").SetIsDefault(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ent.GatewayConnection.Create().SetName("gw-b").SetEndpoint("http://b:4000").Save(ctx); err != nil {
		t.Fatal(err)
	}
	readers := map[string]gateway.SpendReader{
		"gw-a": healthReader{health: &gateway.GatewayHealth{Reachable: true, HealthyCount: 2, Healthy: []gateway.EndpointHealth{{Model: "gpt-4"}, {Model: "gpt-3.5"}}}},
		"gw-b": healthReader{err: context.DeadlineExceeded},
	}
	r.SpendReaderFor = func(_ context.Context, g *ent.GatewayConnection) gateway.SpendReader {
		return readers[g.Name]
	}

	out, err := r.gatewayHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 gateways, got %d", len(out))
	}
	byName := map[string]model.GatewayHealth{}
	for _, g := range out {
		byName[g.GatewayName] = g
	}
	if a := byName["gw-a"]; !a.Reachable || a.HealthyCount != 2 || len(a.Healthy) != 2 {
		t.Errorf("gw-a health wrong: %+v", a)
	}
	if b := byName["gw-b"]; b.Reachable || b.Error == nil {
		t.Errorf("gw-b should be unreachable with an error: %+v", b)
	}
}
