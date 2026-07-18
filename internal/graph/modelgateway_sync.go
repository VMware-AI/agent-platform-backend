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
// surface as a 500 to the create mutation, and the periodic tick (below)
// provides eventual consistency. Audit is intentionally NOT emitted — there
// is no actor; the audit log is for user-driven actions only.
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
// probe → write. Used by both the background goroutine and the unified
// reconciler (gateway_status phase). The caller is responsible for
// inflight-tracking if SYNCING should be visible; this helper does not touch
// the resolver's inflightSyncs map.
//
// Exported so internal/reconcile.Reconciler can call it via the ResolverSource
// interface.
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

// StartModelGatewayAutoSync periodically syncs every GatewayConnection row
// (status, loadBalancingStrategy, backendModelCount). One bad gateway logs
// and continues — a bad row must not block the rest. Disabled when the
// configured interval is 0 — see main.go's wiring guard.
//
// isLeader single-flights the tick across replicas, mirroring the gateway-key
// reconciler (see cmd/server/main.go): every replica otherwise probes each
// gateway and races the status/strategy/count writes on the same row. nil gate
// = always run (dev/single-replica sqlite path, where there is no PG lease).
func (r *Resolver) StartModelGatewayAutoSync(ctx context.Context, interval time.Duration, isLeader func(context.Context) bool) {
	log.Printf("model-gateway auto-sync: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only the leader replica runs the tick body; followers skip so a
			// multi-replica deployment doesn't fan out N× litellm probes and
			// race status writes on the same gateway row.
			if isLeader != nil && !isLeader(ctx) {
				continue
			}
			r.SyncAllGatewaysOnce(ctx)
		}
	}
}

// SyncAllGatewaysOnce fans out one sync pass over every gateway row. Each row is
// loaded + probed + written sequentially — the dataset is small (operator-
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
