package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// StartRouterSettingsSync periodically re-aggregates every ModelRoute
// row and POSTs the per-gateway router_settings payload to /config/update.
// Driven by the LiteLLM design doc §3.2 "原子化路由策略全量覆盖刷新":
// every save also pushes immediately (see
// aggregateAndPushRouterSettings on the resolver side), but this worker is
// the safety net that reconciles drift after an out-of-band edit or a
// transient /config/update failure.
//
// Each ModelRoute is bound to exactly one GatewayConnection
// (route.gateway_connection_id, NOT NULL), and the push is partitioned
// accordingly: one POST per gateway, each carrying the routes bound to
// that gateway only. This is the per-route "→ its litellm" mapping the
// console needs; there is no platform default.
//
// Short-circuit: each (gateway, payload) pair is hashed (SHA-256 of the
// canonical JSON). If the hash matches Resolver.lastRouterSettingsHash
// (a process-local map shared with the resolver-side fire-and-forget
// push), the tick skips the POST — nothing changed for that gateway
// since the last push. On any mismatch — the first-ever tick, or a
// previous push that failed — the tick falls through to the real POST.
// Multi-replica deployments accept the redundant-but-correct first-tick
// push from each replica.
//
// Disabled when interval <= 0.
func (r *Resolver) StartRouterSettingsSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Printf("router settings sync: disabled (ROUTER_SYNC_INTERVAL_SECONDS=0)")
		return
	}
	log.Printf("router settings sync: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.syncRouterSettingsOnceShortCircuit(ctx)
		}
	}
}

// syncRouterSettingsOnceShortCircuit is the periodic-worker's tick body
// and the resolver-side push hook share. It loads active routes, groups
// them by gateway, builds one RouterSettings per gateway, and short-
// circuits on hash match.
//
// Order of operations per gateway:
//  1. Build the payload. On any build-time failure (DB read, etc.) log
//     and skip — don't poison the hash slot.
//  2. Compute the SHA-256 of its canonical JSON.
//  3. If hash matches the recorded baseline, log a short-circuit line
//     and skip. The first-ever call pushes (empty baseline) so every
//     gateway establishes a baseline.
//  4. Resolve the gateway + push. On success, record the new hash. On
//     failure, keep the old baseline → next tick retries.
//
// targetGateway, when non-zero, scopes the entire run to one gateway
// (the resolver-side hook uses this — the route being saved only needs
// its own gateway re-pushed). uuid.Nil means "every gateway".
func (r *Resolver) syncRouterSettingsOnceShortCircuit(ctx context.Context, targetGateway ...uuid.UUID) {
	routesByGW, err := r.loadRouterSettingsBuckets(ctx)
	if err != nil {
		log.Printf("router settings sync: %v", err)
		return
	}

	// If a target gateway is given, only consider its bucket.
	scoped := routesByGW
	if len(targetGateway) == 1 && targetGateway[0] != uuid.Nil {
		bucket, ok := routesByGW[targetGateway[0]]
		if !ok || len(bucket) == 0 {
			return
		}
		scoped = map[uuid.UUID][]*ent.ModelRoute{targetGateway[0]: bucket}
	} else if len(targetGateway) > 1 {
		log.Printf("router settings sync: unexpected variadic targetGateway count %d", len(targetGateway))
		return
	}

	for gwID, routes := range scoped {
		settings := gateway.AggregateRouterSettings(routes, nil)
		hash := hashRouterSettings(settings)
		prev := r.readLastRouterSettingsHash(gwID)
		if prev != "" && hash == prev {
			log.Printf("router settings sync: short-circuit, gateway=%s payload unchanged (%s)", gwID, hash[:12])
			continue
		}
		if err := r.pushRouterSettingsTo(ctx, gwID, settings); err != nil {
			log.Printf("router settings sync: push to %s: %v", gwID, err)
			continue // keep stale baseline → next tick retries
		}
		log.Printf("router settings sync: pushed %s to gateway %s", hash[:12], gwID)
		r.writeLastRouterSettingsHash(gwID, hash)
	}

	// If a targeted run dropped a gateway from the buckets, prune the
	// baseline so we don't accumulate dead entries. (For the periodic
	// full-fleet run this is the gateway-was-deleted case.)
	if len(targetGateway) == 0 {
		r.pruneStaleRouterSettingsHashes(routesByGW)
	}
}

// readLastRouterSettingsHash returns the recorded baseline for gwID, or
// "" if none has been recorded yet. The first-ever call pushes (empty
// baseline) so every gateway establishes itself before any short-circuit
// can apply.
func (r *Resolver) readLastRouterSettingsHash(gwID uuid.UUID) string {
	r.lastRouterSettingsHashMu.Lock()
	defer r.lastRouterSettingsHashMu.Unlock()
	return r.lastRouterSettingsHash[gwID]
}

// writeLastRouterSettingsHash records a successful push baseline.
func (r *Resolver) writeLastRouterSettingsHash(gwID uuid.UUID, hash string) {
	r.lastRouterSettingsHashMu.Lock()
	defer r.lastRouterSettingsHashMu.Unlock()
	if r.lastRouterSettingsHash == nil {
		r.lastRouterSettingsHash = map[uuid.UUID]string{}
	}
	r.lastRouterSettingsHash[gwID] = hash
}

// pruneStaleRouterSettingsHashes removes entries for gateways that no
// longer own any routes so a future re-creation starts with an empty
// baseline (the comparison "hash == prev" can't true-match a stale value
// against a fresh payload).
func (r *Resolver) pruneStaleRouterSettingsHashes(active map[uuid.UUID][]*ent.ModelRoute) {
	r.lastRouterSettingsHashMu.Lock()
	defer r.lastRouterSettingsHashMu.Unlock()
	for gwID := range r.lastRouterSettingsHash {
		if _, ok := active[gwID]; !ok {
			delete(r.lastRouterSettingsHash, gwID)
		}
	}
}

// loadRouterSettingsBuckets loads active routes, partitioning them by
// backendGatewayId. Each bucket is the routes that share a single litellm
// push target. tier-driven routes (RouterTier / complexity router) are no
// longer folded into router_settings — the control plane only knows about
// route-level alias grouping now.
func (r *Resolver) loadRouterSettingsBuckets(ctx context.Context) (routesByGW map[uuid.UUID][]*ent.ModelRoute, err error) {
	routes, qerr := r.Ent.ModelRoute.Query().All(ctx)
	if qerr != nil {
		return nil, fmt.Errorf("query routes: %w", qerr)
	}
	routesByGW = make(map[uuid.UUID][]*ent.ModelRoute, len(routes))
	for _, r := range routes {
		if r == nil {
			continue
		}
		routesByGW[r.ModelGatewayID] = append(routesByGW[r.ModelGatewayID], r)
	}
	return routesByGW, nil
}

// pushRouterSettingsTo resolves a gateway by id, builds a litellm client,
// and POSTs the given payload to /config/update. Errors are returned
// (logged by the caller). The gateway is loaded fresh so a deletion
// between bucket-build and push surfaces as a "not found" error.
func (r *Resolver) pushRouterSettingsTo(ctx context.Context, gwID uuid.UUID, settings gateway.RouterSettings) error {
	g, err := r.Ent.GatewayConnection.Get(ctx, gwID)
	if err != nil {
		if ent.IsNotFound(err) {
			log.Printf("router settings sync: gateway %s deleted between bucket build and push; skipping", gwID)
			return nil
		}
		return err
	}
	mk := r.gatewayMasterKey(ctx, g)
	if mk == "" {
		log.Printf("router settings sync: gateway %s has no resolvable master key; skipping", g.Name)
		return nil
	}
	http, err := gateway.NewHTTPClient(g.Endpoint, mk)
	if err != nil {
		return err
	}
	return gateway.NewAdminClient(http).PushRouterSettings(ctx, settings)
}

// AggregateAndPushRouterSettings triggers a full or scoped push. Reuses the
// same per-gateway hash baseline as the periodic worker so a stable payload
// short-circuits even on the resolver-side hook (which would otherwise fire
// on every save).
//
// targetGateway, when non-zero, scopes the push to a single gateway
// (the resolver-side hook uses this — the route being saved only needs
// its own gateway re-pushed). uuid.Nil means "every gateway that owns
// at least one route", which is what the periodic worker and
// syncRouterSettings use.
func (r *Resolver) AggregateAndPushRouterSettings(ctx context.Context, targetGateway uuid.UUID) {
	if targetGateway == uuid.Nil {
		r.syncRouterSettingsOnceShortCircuit(ctx)
		return
	}
	r.syncRouterSettingsOnceShortCircuit(ctx, targetGateway)
}

// AggregateAndPushRouterSettingsFireAndForget schedules a router-settings
// push on a detached background goroutine after a route mutation. We
// detach the context (the original may already be canceled/timed-out —
// e.g. a client-side cancellation that races the post-save hook) and
// forget the goroutine; the periodic worker is the safety net for any
// push that fails or gets dropped.
//
// targetGateway, when non-zero, scopes the push to that gateway. uuid.Nil
// means "push every gateway that owns at least one active route" (used
// after non-route mutations, e.g. when the operator re-syncs the whole
// fleet explicitly).
func (r *Resolver) AggregateAndPushRouterSettingsFireAndForget(targetGateway uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.AggregateAndPushRouterSettings(ctx, targetGateway)
	}()
}

// bucketRoutesByGateway groups routes by their model_gateway_id —
// the same partition used by the router sync worker. Exposed so future
// caller paths (e.g. an on-demand push hook) can iterate per gateway.
func bucketRoutesByGateway(routes []*ent.ModelRoute) map[uuid.UUID][]*ent.ModelRoute {
	out := make(map[uuid.UUID][]*ent.ModelRoute, len(routes))
	for _, r := range routes {
		if r == nil {
			continue
		}
		out[r.ModelGatewayID] = append(out[r.ModelGatewayID], r)
	}
	return out
}

// hashRouterSettings canonicalises the payload to JSON and returns the
// SHA-256 hex. Only the inner RouterSettings is hashed — the wrapper
// {"router_settings": ...} is static for our purposes and adds no
// entropy while nearly doubling the byte length.
//
// json.Marshal is sufficient (not json.Marshal with map-key sort): Go's
// encoding/json emits struct fields in declaration order, slice elements
// in order, and string-map keys in lexical order. The aggregation
// pipeline (gateway.AggregateRouterSettings) builds the slices
// deterministically — the same DB rows in the same order produce
// byte-identical JSON, so the hash is stable across calls whenever the
// underlying data is.
func hashRouterSettings(s gateway.RouterSettings) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a struct of basic types / []T / map[string]string
		// cannot fail in practice; if it ever does, fall back to a sentinel
		// that is guaranteed NOT to match any real baseline so the next tick
		// pushes (we'd rather push a duplicate than miss a real change).
		return "marshal-error"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
