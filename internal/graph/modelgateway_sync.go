package graph

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

// syncGatewayInBackground fires a goroutine that probes the litellm gateway
// and persists status / loadBalancingStrategy / backendModelCount on the row.
// A fresh context.Background (with a 30s timeout) is used so the sync outlives
// the GraphQL request that triggered it — the create mutation returns
// immediately, and the user refreshing the list moments later sees the
// populated values.
//
// Errors are logged and swallowed: a slow / unreachable gateway must not
// surface as a 500 to the create mutation, and the unified reconciler
// provides eventual consistency via its gateway_status phase. Audit is
// intentionally NOT emitted — there is no actor; the audit log is for
// user-driven actions only.
//
// The returned channel is closed when the goroutine finishes, so tests can
// wait deterministically on the post-create sync. Production callers can
// ignore the channel.
func (r *Resolver) syncGatewayInBackground(id uuid.UUID) chan struct{} {
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.SyncGatewayStatusOnce(ctx, id)
		close(done)
	}()
	return done
}

// SyncGatewayStatusOnce runs one sync pass for a single gateway id: load →
// probe → write. Used by both the background goroutine (post-create hook)
// and the unified reconciler's gateway_status phase. The caller is
// responsible for inflight-tracking if SYNCING should be visible; this
// helper does not touch the resolver's inflightSyncs map.
//
// Exported so internal/reconcile.Reconciler can call it via the ResolverSource
// interface.
//
// (PR #3 cut-over: the previous StartModelGatewayAutoSync ticker that
// fan-out this over every gateway is gone — the unified reconciler's
// gateway_status phase does that per-cycle now. SyncGatewayStatusOnce
// remains as the per-gateway workhorse; SyncAllGatewaysOnce remains as
// the fleet-wide fan-out helper, exported for test/future-callers.)
func (r *Resolver) SyncGatewayStatusOnce(ctx context.Context, id uuid.UUID) {
	g, err := r.Ent.GatewayConnection.Get(ctx, id)
	if err != nil {
		log.Printf("model-gateway background sync: load %s: %v", id, err)
		return
	}
	mgr := r.buildGatewayModels(ctx, g)
	status, strategy := probeGatewayConnection(ctx, mgr)
	count, ok := probeGatewayBackendModelCount(ctx, mgr)
	var cntArg *int
	if ok {
		cntArg = &count
	}
	if _, err := r.applyGatewayTestResult(ctx, g, status, cntArg, strategy); err != nil {
		log.Printf("model-gateway background sync: persist %s: %v", id, err)
	}
}

// SyncAllGatewaysOnce fans out one sync pass over every gateway row. Each row
// is loaded + probed + written sequentially — the dataset is small (operator-
// curated), so the cost of N HTTP probes against litellm is bounded and
// parallelism would mostly just hammer the litellm box. If that ever changes,
// wrap the loop in a worker pool.
//
// Exported so internal/reconcile.Reconciler can call it via ResolverSource if
// a fleet-wide (not per-gateway) gateway_status refresh is desired. The
// unified cycle currently uses SyncGatewayStatusOnce per-gateway instead.
func (r *Resolver) SyncAllGatewaysOnce(ctx context.Context) {
	gws, err := r.Ent.GatewayConnection.Query().All(ctx)
	if err != nil {
		log.Printf("model-gateway auto-sync: query: %v", err)
		return
	}
	for _, g := range gws {
		// Track in-flight so concurrent list / summary reads see SYNCING.
		r.beginSync(g.ID)
		func() {
			defer r.endSync(g.ID)
			mgr := r.buildGatewayModels(ctx, g)
			status, strategy := probeGatewayConnection(ctx, mgr)
			count, ok := probeGatewayBackendModelCount(ctx, mgr)
			var cntArg *int
			if ok {
				cntArg = &count
			}
			if _, err := r.applyGatewayTestResult(ctx, g, status, cntArg, strategy); err != nil {
				log.Printf("model-gateway auto-sync: persist %s: %v", g.ID, err)
			}
		}()
	}
}
