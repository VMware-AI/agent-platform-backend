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
// canonical JSON). If it matches the last *successfully pushed* payload
// for that gateway from this process, the tick skips the POST — nothing
// changed for that gateway since the last push. On any mismatch — the
// first-ever tick, or a previous fire-and-forget push that failed — the
// tick falls through to the real POST. The per-gateway hash lives in a
// closure so each start gets a fresh baseline; the worker pays exactly
// one push per gateway on the first tick, then skips as long as the
// payload for that gateway stays stable.
//
// Disabled when interval <= 0.
func (r *Resolver) StartRouterSettingsSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Printf("router settings sync: disabled (ROUTER_SYNC_INTERVAL_SECONDS=0)")
		return
	}
	log.Printf("router settings sync: every %s", interval)
	// lastPushedHash is the per-gateway SHA-256 hex of the last payload
	// POSTed successfully from this process. Keyed by gateway id; missing
	// entry means "no baseline yet for that gateway → push on first tick".
	// Read/written only on this goroutine, so no lock needed.
	lastPushedHash := map[uuid.UUID]string{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastPushedHash = r.syncRouterSettingsOnceShortCircuit(ctx, lastPushedHash)
		}
	}
}

// syncRouterSettingsOnce runs a single sync pass: load active routes, group
// routes by backendGatewayId, build one RouterSettings per gateway, and POST
// to /config/update. Errors are logged but not surfaced — this is a safety
// net, not a request-driven operation.
func (r *Resolver) syncRouterSettingsOnce(ctx context.Context) {
	routesByGW, err := r.loadRouterSettingsBuckets(ctx)
	if err != nil {
		log.Printf("router settings sync: %v", err)
		return
	}
	if len(routesByGW) == 0 {
		return
	}
	for gwID, routes := range routesByGW {
		settings := gateway.AggregateRouterSettings(routes, nil)
		if err := r.pushRouterSettingsTo(ctx, gwID, settings); err != nil {
			log.Printf("router settings sync: push to %s: %v", gwID, err)
		}
	}
}

// syncRouterSettingsOnceShortCircuit is the periodic-worker's tick body.
// It hashes the per-gateway payload and skips the POST when the hash
// matches the previously-successful push from this process for that
// gateway. Returns the new hash map.
//
// Order of operations per gateway:
//  1. Build the payload. On any build-time failure (DB read, etc.) log
//     and keep the existing baseline — don't poison the hash slot.
//  2. Compute the SHA-256 of its canonical JSON.
//  3. If hash matches lastPushedHash AND lastPushedHash is non-empty, log
//     a short-circuit line and skip. The first-ever tick pushes (empty
//     baseline) so every gateway establishes a baseline.
//  4. Resolve the gateway + push. On success, record the new hash. On
//     failure, keep the old baseline → next tick retries.
func (r *Resolver) syncRouterSettingsOnceShortCircuit(ctx context.Context, lastPushedHash map[uuid.UUID]string) map[uuid.UUID]string {
	routesByGW, err := r.loadRouterSettingsBuckets(ctx)
	if err != nil {
		log.Printf("router settings sync: %v", err)
		return lastPushedHash
	}
	for gwID, routes := range routesByGW {
		settings := gateway.AggregateRouterSettings(routes, nil)
		hash := hashRouterSettings(settings)
		prev, has := lastPushedHash[gwID]
		if has && hash == prev {
			log.Printf("router settings sync: short-circuit, gateway=%s payload unchanged (%s)", gwID, hash[:12])
			continue
		}
		if err := r.pushRouterSettingsTo(ctx, gwID, settings); err != nil {
			log.Printf("router settings sync: push to %s: %v", gwID, err)
			continue // keep stale baseline → next tick retries
		}
		log.Printf("router settings sync: pushed %s to gateway %s", hash[:12], gwID)
		lastPushedHash[gwID] = hash
	}
	// Drop hash entries for gateways with no active routes (they were
	// deleted) so a future re-creation starts with an empty baseline.
	for gwID := range lastPushedHash {
		if _, ok := routesByGW[gwID]; !ok {
			delete(lastPushedHash, gwID)
		}
	}
	return lastPushedHash
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

// AggregateAndPushRouterSettings triggers a full sync across every gateway
// that owns at least one active model route. Pulled out for testability
// and to share between the periodic worker and the resolver-side hook
// that runs after a route mutation.
//
// targetGateway, when non-zero, scopes the push to a single gateway
// (the resolver-side hook uses this — the route being saved only needs
// its own gateway re-pushed). uuid.Nil means "push every gateway that
// owns at least one active route", which is what the periodic worker
// calls.
func (r *Resolver) AggregateAndPushRouterSettings(ctx context.Context, targetGateway uuid.UUID) {
	routesByGW, err := r.loadRouterSettingsBuckets(ctx)
	if err != nil {
		log.Printf("router settings sync: %v", err)
		return
	}
	if len(routesByGW) == 0 {
		return
	}
	if targetGateway != uuid.Nil {
		routes := routesByGW[targetGateway]
		if len(routes) == 0 {
			// The route's gateway was deleted, or all of its routes
			// were disabled, between save and push. Nothing to do.
			return
		}
		settings := gateway.AggregateRouterSettings(routes, nil)
		if err := r.pushRouterSettingsTo(ctx, targetGateway, settings); err != nil {
			log.Printf("router settings sync: push to %s: %v", targetGateway, err)
		}
		return
	}
	for gwID, routes := range routesByGW {
		settings := gateway.AggregateRouterSettings(routes, nil)
		if err := r.pushRouterSettingsTo(ctx, gwID, settings); err != nil {
			log.Printf("router settings sync: push to %s: %v", gwID, err)
		}
	}
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

// bucketRoutesByGateway groups routes by their gateway_connection_id —
// the same partition used by the router sync worker. Exposed so the
// SyncRouterSettings mutation can target each gateway individually.
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
