package graph

import (
	"context"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/department"
	"github.com/VMware-AI/agent-platform-backend/ent/gatewayconnection"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func toModelGatewayConnection(g *ent.GatewayConnection) *model.GatewayConnection {
	var publicURL *string
	if g.PublicURL != "" {
		publicURL = &g.PublicURL
	}
	return &model.GatewayConnection{
		ID:                  g.ID.String(),
		Name:                g.Name,
		Endpoint:            g.Endpoint,
		PublicURL:           publicURL,
		IsDefault:           g.IsDefault,
		Status:              model.GatewayStatus(string(g.Status)),
		LoadBalanceStrategy: model.LoadBalanceStrategy(string(g.LoadBalanceStrategy)),
		CreatedAt:           g.CreatedAt,
	}
}

// ---- 模型网关页 (P4): GatewayConnection façade helpers ----

// modelGatewayStatus maps the ent connection status to the console status enum.
func modelGatewayStatus(s gatewayconnection.Status) model.ModelGatewayStatus {
	switch s {
	case gatewayconnection.StatusConnected:
		return model.ModelGatewayStatusConnected
	case gatewayconnection.StatusError:
		return model.ModelGatewayStatusError
	default:
		return model.ModelGatewayStatusDisconnected
	}
}

// entGatewayStatus maps the console status enum back to the ent column (filter).
func entGatewayStatus(s model.ModelGatewayStatus) gatewayconnection.Status {
	switch s {
	case model.ModelGatewayStatusConnected:
		return gatewayconnection.StatusConnected
	case model.ModelGatewayStatusError:
		return gatewayconnection.StatusError
	default:
		return gatewayconnection.StatusDisconnected
	}
}

// modelGatewaySyncState derives the sync state from the connection status — there
// is no dedicated sync-tracking store yet: connected→SYNCED, error→FAILED,
// disconnected→NEVER (never synced).
func modelGatewaySyncState(s gatewayconnection.Status) model.ModelGatewaySyncState {
	switch s {
	case gatewayconnection.StatusConnected:
		return model.ModelGatewaySyncStateSynced
	case gatewayconnection.StatusError:
		return model.ModelGatewaySyncStateFailed
	default:
		return model.ModelGatewaySyncStateNever
	}
}

// toModelGateway projects a GatewayConnection (+ the live backend-model count) into
// the console's ModelGateway aggregate. provider/strategy are the console's single
// supported values; adminUrl is the operator-set admin_url, falling back to the
// derived <endpoint>/ui; latencyMs is transient (only a live test sets it);
// lastSyncAt is the real last_synced_at column (nil until the gateway has ever
// successfully connected), and never moves on an unrelated edit.
func toModelGateway(g *ent.GatewayConnection, backendModelCount int) *model.ModelGateway {
	adminURL := g.AdminURL
	if adminURL == "" {
		adminURL = strings.TrimRight(g.Endpoint, "/") + "/ui"
	}
	return &model.ModelGateway{
		ID:                    g.ID.String(),
		Name:                  g.Name,
		Provider:              model.ModelGatewayProviderLitellm,
		Endpoint:              g.Endpoint,
		Status:                modelGatewayStatus(g.Status),
		BackendModelCount:     backendModelCount,
		LoadBalancingStrategy: model.LoadBalancingStrategyRoundRobin,
		AdminURL:              &adminURL,
		LastSyncAt:            g.LastSyncedAt,
		LastSyncStatus:        modelGatewaySyncState(g.Status),
		CreatedAt:             g.CreatedAt,
		UpdatedAt:             g.UpdatedAt,
	}
}

// applyModelGatewaySort orders a gateway query by the console's sort field with a
// stable id tiebreak; default (and any unmapped field) is newest-first.
func applyModelGatewaySort(q *ent.GatewayConnectionQuery, sort *model.ModelGatewaySort) *ent.GatewayConnectionQuery {
	if sort == nil {
		return q.Order(ent.Desc(gatewayconnection.FieldCreatedAt))
	}
	desc := sort.Direction == model.SortDirectionDesc
	col := gatewayconnection.FieldCreatedAt
	switch sort.Field {
	case model.ModelGatewaySortFieldName:
		col = gatewayconnection.FieldName
	case model.ModelGatewaySortFieldEndpoint:
		col = gatewayconnection.FieldEndpoint
	case model.ModelGatewaySortFieldStatus:
		col = gatewayconnection.FieldStatus
	case model.ModelGatewaySortFieldUpdatedAt:
		col = gatewayconnection.FieldUpdatedAt
	default: // CREATED_AT
		col = gatewayconnection.FieldCreatedAt
	}
	if desc {
		return q.Order(ent.Desc(col), ent.Desc(gatewayconnection.FieldID))
	}
	return q.Order(ent.Asc(col), ent.Asc(gatewayconnection.FieldID))
}

// backendModelCount is the number of registered upstreams the litellm gateway
// fronts (real DB count, surfaced as ModelGateway.backendModelCount).
func (r *Resolver) backendModelCount(ctx context.Context) (int, error) {
	return r.Ent.Upstream.Query().Count(ctx)
}

// buildGatewayModels builds a litellm model-manager bound to a SPECIFIC gateway
// row — its own endpoint and master key (resolved from the secret store) — so a
// model/connection op hits the right gateway, not a process-wide default. The
// builder is injectable (GatewayClientFor) so tests can supply a fake.
func (r *Resolver) buildGatewayModels(ctx context.Context, g *ent.GatewayConnection) gateway.ModelManager {
	masterKey := r.gatewayMasterKey(ctx, g)
	if r.GatewayClientFor != nil {
		return r.GatewayClientFor(ctx, g.Endpoint, masterKey)
	}
	return gateway.NewHTTPClient(g.Endpoint, masterKey)
}

// applyGatewayTestResult persists a connection-test outcome on a gateway row: it
// sets the status and, on a successful connect, stamps last_synced_at = now.
// Both gateway façades (ModelGateway + the legacy routing GatewayConnection) go
// through here so they agree on "last synced" — a successful test from either the
// 模型网关接入 page or the 模型路由 page advances the same timestamp.
func (r *Resolver) applyGatewayTestResult(ctx context.Context, g *ent.GatewayConnection, status gatewayconnection.Status) (*ent.GatewayConnection, error) {
	upd := r.Ent.GatewayConnection.UpdateOne(g).SetStatus(status)
	if status == gatewayconnection.StatusConnected {
		upd.SetLastSyncedAt(time.Now())
	}
	return upd.Save(ctx)
}

// assertGatewayDeletable refuses to delete a GatewayConnection that is still
// referenced by a department (gateway_connection_id is a soft FK — deleting
// would silently break those departments' key/team ops, LLD-13 §3.3) or that is
// the platform default (the platform must always keep one). Shared by both
// delete façades (DeleteGatewayConnection + DeleteModelGateway) so they enforce
// the same guards.
func (r *Resolver) assertGatewayDeletable(ctx context.Context, g *ent.GatewayConnection) error {
	if n, err := r.Ent.Department.Query().Where(department.GatewayConnectionID(g.ID)).Count(ctx); err != nil {
		return err
	} else if n > 0 {
		return gqlerror.Errorf("gateway is used by %d department(s); reassign them before deleting", n)
	}
	if g.IsDefault {
		return gqlerror.Errorf("cannot delete the default gateway; set another default first")
	}
	return nil
}
