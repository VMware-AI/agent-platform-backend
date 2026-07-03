package graph

import (
	"context"
	"sync"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// gatewayHealth fans out to every configured gateway's litellm /health and
// returns each gateway's upstream health (LLD-15 T4). An unreachable gateway
// degrades to reachable=false + an error string rather than failing the query.
func (r *Resolver) gatewayHealth(ctx context.Context) ([]model.GatewayHealth, error) {
	conns, err := r.Ent.GatewayConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.GatewayHealth, len(conns))
	var wg sync.WaitGroup
	for i, g := range conns {
		wg.Add(1)
		go func(i int, g *ent.GatewayConnection) {
			defer wg.Done()
			out[i] = r.oneGatewayHealth(ctx, g)
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
