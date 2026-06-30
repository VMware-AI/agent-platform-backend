package graph

import (
	"context"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/department"
	"github.com/VMware-AI/agent-platform-backend/ent/gatewayconnection"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
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
		LoadBalanceStrategy: model.LoadBalancingStrategy(string(g.LoadBalanceStrategy)),
		CreatedAt:           g.CreatedAt,
	}
}

// ---- 模型网关页 (P4): GatewayConnection façade helpers ----

// probeGatewayConnection runs the connectivity test + best-effort strategy
// probe against a pre-built ModelManager. Used by the id-based post-create
// syncModelGatewayConnection — the only flow that surfaces the live routing
// strategy for the user to read off the row. Returns the projected status and
// the probed strategy (nil on probe failure). Probe errors are logged
// server-side, never returned to the caller.
func probeGatewayConnection(ctx context.Context, mgr gateway.ModelManager) (gatewayconnection.Status, *model.LoadBalancingStrategy) {
	status := probeGatewayConnectionStatus(ctx, mgr)
	if status != gatewayconnection.StatusConnected {
		return status, nil
	}
	rs, err := mgr.GetRoutingStrategy(ctx)
	if err != nil {
		log.Printf("model gateway routing-strategy probe failed: %v", err)
		return status, nil
	}
	mapped := mapRoutingStrategy(rs)
	return status, &mapped
}

// probeGatewayConnectionStatus runs the connectivity test only. Used by the
// pre-create testNewModelGatewayConnection, which deliberately does NOT probe
// the routing strategy — that field on the result is always null in the
// dry-run flow. Returns the projected status. The transport error is logged
// server-side, never returned to the caller.
func probeGatewayConnectionStatus(ctx context.Context, mgr gateway.ModelManager) gatewayconnection.Status {
	status := gatewayconnection.StatusConnected
	err := mgr.TestConnection(ctx)
	if err != nil {
		status = gatewayconnection.StatusError
		log.Printf("model gateway probe failed: %v", err)
	}
	return status
}

// probeGatewayBackendModelCount reads the gateway's current model count via
// GET /models and returns (count, true) on success. On any failure it logs
// the error and returns (0, false) so the caller can preserve the previously-
// stored count instead of clobbering it with a transient zero.
func probeGatewayBackendModelCount(ctx context.Context, mgr gateway.ModelManager) (int, bool) {
	models, err := mgr.ListModels(ctx)
	if err != nil {
		log.Printf("model gateway backend-model-count probe failed: %v", err)
		return 0, false
	}
	return len(models), true
}

// mapRoutingStrategy is the SOLE inbound adapter for the litellm routing
// strategy: it translates the wire-level RoutingStrategy (litellm returns
// kebab-case values: `simple-shuffle`, `least-busy`, ...) into the console's
// GraphQL LoadBalancingStrategy enum (UPPER_SNAKE_CASE, shared verbatim with
// the ent column). Both the wire and the GraphQL/DB side hold the same 5
// strategies; this is the only place the spelling differs. The deprecated
// `usage-based-routing` (pre-v2) wire value is normalised to
// USAGE_BASED_ROUTING_V2 — same strategy, just the older name.
func mapRoutingStrategy(rs gateway.RoutingStrategy) model.LoadBalancingStrategy {
	switch rs {
	case "simple-shuffle":
		return model.LoadBalancingStrategySimpleShuffle
	case "least-busy":
		return model.LoadBalancingStrategyLeastBusy
	case "latency-based-routing":
		return model.LoadBalancingStrategyLatencyBasedRouting
	case "usage-based-routing", "usage-based-routing-v2":
		return model.LoadBalancingStrategyUsageBasedRoutingV2
	case "cost-based-routing":
		return model.LoadBalancingStrategyCostBasedRouting
	default:
		// Unreachable from HTTPClient.GetRoutingStrategy (it returns
		// ErrUnknownRoutingStrategy for unmapped wire values), but defend
		// against a future caller that bypasses that path.
		return model.LoadBalancingStrategySimpleShuffle
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

// r_isSyncing reports whether a gateway id is currently being synced on this
// process. Used by toModelGateway to overlay SYNCING on the lastSyncStatus
// projection. nil-safe: a nil inflight map (older tests don't construct one)
// returns false.
//
// Kept on *Resolver (rather than as a free function) so tests can swap in a
// resolver with their own inflight map; the SYNCING overlay applies only
// when a resolver is configured with a non-nil inflightSyncs.

// modelGatewaySyncStateForRead extends modelGatewaySyncState with the
// "currently syncing" overlay: a gateway whose ID is in the resolver's
// in-flight sync map is reported as SYNCING regardless of its stored status,
// so a manual sync button or auto-sync tick surfaces the in-progress state
// to the UI without a separate persisted column.
func modelGatewaySyncStateForRead(s gatewayconnection.Status, inSync bool) model.ModelGatewaySyncState {
	if inSync {
		return model.ModelGatewaySyncStateSyncing
	}
	return modelGatewaySyncState(s)
}

// toModelGateway projects a GatewayConnection into the console's ModelGateway
// aggregate. Pure column projection — backendModelCount / loadBalancingStrategy
// are read directly from the ent columns updated by sync, so this projection
// is consistent between list reads and post-sync responses. lastSyncStatus is
// derived from the connection status, overridden to SYNCING when the gateway
// id is currently in the resolver's in-flight sync map.
func (r *Resolver) toModelGateway(g *ent.GatewayConnection) *model.ModelGateway {
	var strategy *model.LoadBalancingStrategy
	if g.LoadBalanceStrategy != "" {
		s := model.LoadBalancingStrategy(string(g.LoadBalanceStrategy))
		strategy = &s
	}
	var backendModelCount int
	if g.BackendModelCount != nil {
		backendModelCount = *g.BackendModelCount
	}
	return &model.ModelGateway{
		ID:                    g.ID.String(),
		Name:                  g.Name,
		Provider:              model.ModelGatewayProviderLitellm,
		Endpoint:              g.Endpoint,
		BackendModelCount:     backendModelCount,
		LoadBalancingStrategy: strategy,
		LastSyncAt:            g.LastSyncedAt,
		LastSyncStatus:        modelGatewaySyncStateForRead(g.Status, r.isSyncing(g.ID)),
		CreatedAt:             g.CreatedAt,
		UpdatedAt:             g.UpdatedAt,
	}
}

// isSyncing reports whether a gateway id is currently being synced on this
// process. nil-safe: a nil inflight map (older tests don't construct one)
// returns false.
func (r *Resolver) isSyncing(id uuid.UUID) bool {
	if r == nil || r.inflightSyncs == nil {
		return false
	}
	r.inflightSyncsMu.Lock()
	defer r.inflightSyncsMu.Unlock()
	_, ok := r.inflightSyncs[id]
	return ok
}

// beginSync marks a gateway id as in-flight; the caller MUST defer endSync.
// Used to drive the SYNCING overlay on lastSyncStatus.
func (r *Resolver) beginSync(id uuid.UUID) {
	if r == nil {
		return
	}
	r.inflightSyncsMu.Lock()
	defer r.inflightSyncsMu.Unlock()
	if r.inflightSyncs == nil {
		r.inflightSyncs = map[uuid.UUID]struct{}{}
	}
	r.inflightSyncs[id] = struct{}{}
}

// endSync clears a gateway id from the in-flight set. Safe to call without
// a prior beginSync.
func (r *Resolver) endSync(id uuid.UUID) {
	if r == nil {
		return
	}
	r.inflightSyncsMu.Lock()
	defer r.inflightSyncsMu.Unlock()
	delete(r.inflightSyncs, id)
}

// testResultMessage maps a success bool to the console-facing message. Both
// the id-based and the dry-run test resolvers return the same two messages —
// the raw transport error is logged server-side and never reaches the client.
func testResultMessage(ok bool) string {
	if ok {
		return "connection ok"
	}
	return "connection failed"
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

// buildGatewayModels builds a litellm model-manager bound to a SPECIFIC gateway
// row — its own endpoint and master key (resolved from the secret store) — so a
// model/connection op hits the right gateway, not a process-wide default. The
// builder is injectable (GatewayClientFor) so tests can supply a fake.
//
// If client construction fails (vault master key empty, bad endpoint) we
// return a stub whose every method returns a typed error rather than nil. This
// keeps the call sites panic-free: probe / list / op paths read a normal
// error and surface it (or log it server-side for best-effort probes), instead
// of a nil-pointer panic in a goroutine.
func (r *Resolver) buildGatewayModels(ctx context.Context, g *ent.GatewayConnection) gateway.ModelManager {
	masterKey := r.gatewayMasterKey(ctx, g)
	if r.GatewayClientFor != nil {
		return r.GatewayClientFor(ctx, g.Endpoint, masterKey)
	}
	c, err := gateway.NewHTTPClient(g.Endpoint, masterKey)
	if err != nil {
		log.Printf("model gateway client build failed for %s: %v", g.ID, err)
		return &errModelManager{err: err}
	}
	return c
}

// errModelManager satisfies gateway.ModelManager by returning a fixed error
// from every method. Used as a sentinel when buildGatewayModels can't build a
// real client — probes / ops see a normal error instead of nil.
type errModelManager struct{ err error }

func (e *errModelManager) TestConnection(context.Context) error    { return e.err }
func (e *errModelManager) GetRoutingStrategy(context.Context) (gateway.RoutingStrategy, error) {
	return "", e.err
}
func (e *errModelManager) ListModels(context.Context) ([]gateway.ModelInfo, error) {
	return nil, e.err
}
func (e *errModelManager) NewModel(context.Context, gateway.ModelSpec) error   { return e.err }
func (e *errModelManager) DeleteModel(context.Context, string) error            { return e.err }
func (e *errModelManager) UpsertComplexityRouter(context.Context, gateway.RouterSpec) error {
	return e.err
}

// applyGatewayTestResult persists a connection-test outcome on a gateway row.
// It sets status, last_synced_at, and — when the caller supplies a non-nil
// backendModelCount / strategy — also writes those. Nil pointers mean "the
// probe failed, preserve the stored value": a transient outage must not
// zero the displayed count or reset the strategy. Shared by the ModelGateway
// sync façade AND the legacy GatewayConnection test façade (which always
// passes nil, nil — legacy test does not probe count / strategy).
func (r *Resolver) applyGatewayTestResult(
	ctx context.Context,
	g *ent.GatewayConnection,
	status gatewayconnection.Status,
	backendModelCount *int,
	strategy *model.LoadBalancingStrategy,
) (*ent.GatewayConnection, error) {
	upd := r.Ent.GatewayConnection.UpdateOne(g).SetStatus(status)
	if status == gatewayconnection.StatusConnected {
		upd.SetLastSyncedAt(time.Now())
	}
	if backendModelCount != nil {
		upd.SetBackendModelCount(*backendModelCount)
	}
	if strategy != nil {
		upd.SetLoadBalanceStrategy(gatewayconnection.LoadBalanceStrategy(*strategy))
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
	// Don't orphan active billable keys: a non-revoked key minted on this gateway
	// (gateway_connection_id) can only be revoked on it (LLD-14), so deleting the
	// gateway would strand it. Revoke/recycle those agents first.
	if n, err := r.Ent.VirtualKey.Query().
		Where(virtualkey.GatewayConnectionID(g.ID), virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
		Count(ctx); err != nil {
		return err
	} else if n > 0 {
		return gqlerror.Errorf("gateway has %d active virtual key(s); revoke or recycle those agents before deleting", n)
	}
	if g.IsDefault {
		return gqlerror.Errorf("cannot delete the default gateway; set another default first")
	}
	return nil
}

// deleteGatewayByID runs the shared delete sequence both gateway façades use:
// parse → load → deletable-guard → delete row → retire the master-key secret
// (best-effort; the row is already gone). The two façade resolvers wrap this
// with their own return projection + audit action string — the ONLY way the
// delete paths differ. Audit stays at the call site (success-only) so each keeps
// its distinct action ("model_gateway.delete" vs "gateway.delete"). The test and
// create/register paths are deliberately NOT shared — their status-default and
// is_default-singleton logic genuinely differ (see modelgateway/gateway-routing).
func (r *Resolver) deleteGatewayByID(ctx context.Context, id string) error {
	gid, err := uuid.Parse(id)
	if err != nil {
		return gqlerror.Errorf("invalid id")
	}
	g, err := r.Ent.GatewayConnection.Get(ctx, gid)
	if err != nil {
		return err
	}
	if err := r.assertGatewayDeletable(ctx, g); err != nil {
		return err
	}
	if err := r.Ent.GatewayConnection.DeleteOneID(gid).Exec(ctx); err != nil {
		return err
	}
	r.deleteSecretRef(ctx, g.MasterKeyRef) // best-effort; row is gone
	return nil
}
