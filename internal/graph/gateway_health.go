package graph

import (
	"context"
	"sync"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// gatewayHealthTimeout bounds the whole health fan-out. Without it, each probe
// is capped only by the gateway client's ~15s HTTP timeout × GET retry attempts
// (~45s) and wg.Wait() blocks on the slowest gateway, so a single hung gateway
// pushes the query toward the 60s server WriteTimeout. 5s keeps the query snappy
// while still tolerating a normal /health round-trip; a gateway that exceeds it
// degrades to an error entry (see oneGatewayHealth), never a 500.
const gatewayHealthTimeout = 5 * time.Second

// gatewayHealth fans out to every configured gateway's litellm /health and
// returns each gateway's upstream health (LLD-15 T4). An unreachable (or slow)
// gateway degrades to reachable=false + an error string rather than failing the
// query.
func (r *Resolver) gatewayHealth(ctx context.Context) ([]model.GatewayHealth, error) {
	conns, err := r.Ent.GatewayConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	// Cap the fan-out so one hung gateway can't drag wg.Wait() out to the server
	// WriteTimeout. Each probe shares this deadline; an exceeded probe surfaces as
	// that gateway's error entry, preserving partial-failure degradation.
	fanoutCtx, cancel := context.WithTimeout(ctx, gatewayHealthTimeout)
	defer cancel()
	out := make([]model.GatewayHealth, len(conns))
	var wg sync.WaitGroup
	for i, g := range conns {
		wg.Add(1)
		go func(i int, g *ent.GatewayConnection) {
			defer wg.Done()
			out[i] = r.oneGatewayHealth(fanoutCtx, g)
		}(i, g)
	}
	wg.Wait()
	return out, nil
}

func (r *Resolver) oneGatewayHealth(ctx context.Context, g *ent.GatewayConnection) model.GatewayHealth {
	gh := model.GatewayHealth{GatewayID: g.ID.String(), GatewayName: g.Name}
	reader, err := r.buildSpendReader(ctx, g)
	if err != nil {
		msg := err.Error()
		gh.Error = &msg
		return gh
	}
	health, err := reader.Health(ctx)
	if err != nil {
		msg := err.Error()
		gh.Error = &msg
		return gh
	}
	gh.Reachable = health.Reachable
	gh.HealthyCount = health.HealthyCount
	gh.UnhealthyCount = health.UnhealthyCount
	gh.Healthy = toEndpointHealth(health.Healthy)
	gh.Unhealthy = toEndpointHealth(health.Unhealthy)
	return gh
}

func toEndpointHealth(in []gateway.EndpointHealth) []model.EndpointHealth {
	out := make([]model.EndpointHealth, 0, len(in))
	for _, e := range in {
		ep := model.EndpointHealth{Model: e.Model}
		if e.APIBase != "" {
			base := e.APIBase
			ep.APIBase = &base
		}
		out = append(out, ep)
	}
	return out
}
