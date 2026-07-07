package graph

import (
	"context"
	"log"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/gatewayconnection"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/reconcile"
)

// gatewayMasterKey resolves a connection's litellm master key from the secret
// store (master_key_ref). Empty when unset or unresolvable.
func (r *Resolver) gatewayMasterKey(ctx context.Context, g *ent.GatewayConnection) string {
	if g.MasterKeyRef != "" && r.Secrets != nil {
		if cred, err := r.resolveSecret(ctx, g.MasterKeyRef, secretPurposeGatewayMaster); err == nil {
			return cred.APIKey
		}
	}
	return ""
}

// buildGatewayKeyClient builds a litellm key/team client bound to a SPECIFIC
// gateway row. Injectable (GatewayKeyClientFor) for tests; nil → a real client.
// Returns nil on construction failure (empty master key / bad endpoint) so the
// caller treats it as "no client configured" rather than panicking.
func (r *Resolver) buildGatewayKeyClient(ctx context.Context, g *ent.GatewayConnection) gateway.Client {
	if r.GatewayKeyClientFor != nil {
		return r.GatewayKeyClientFor(ctx, g)
	}
	c, err := gateway.NewHTTPClient(g.Endpoint, r.gatewayMasterKey(ctx, g))
	if err != nil {
		log.Printf("gateway key client build failed for %s: %v", g.ID, err)
		return nil
	}
	return c
}

// routeGateway resolves the GatewayConnection that hosts a route's
// router-settings push. A route's gateway_connection_id is the canonical
// push target; department binding / virtual key issuance follow a separate
// resolution path (resolveKeyGateway). nil when the row is missing.
func (r *Resolver) routeGateway(ctx context.Context, route *ent.ModelRoute) (*ent.GatewayConnection, error) {
	if route == nil {
		return nil, nil
	}
	return r.Ent.GatewayConnection.Get(ctx, route.GatewayConnectionID)
}

// resolveDeptGateway picks the GatewayConnection that should serve a
// department's litellm ops (LLD-13 §3.3): the department's own
// gateway_connection_id if set. Returns (nil, nil) when no department
// binding exists — callers that need a gateway must surface this as an
// error rather than silently routing to a fallback (no platform default
// exists anymore; the router-settings push target is the route's own
// backend_gateway_id).
func (r *Resolver) resolveDeptGateway(ctx context.Context, deptID *uuid.UUID) (*ent.GatewayConnection, error) {
	if deptID != nil {
		d, err := r.Ent.Department.Get(ctx, *deptID)
		switch {
		case err != nil && !ent.IsNotFound(err):
			return nil, err // a real DB error must surface, not silently route
		case err == nil && d.GatewayConnectionID != nil:
			// The department is explicitly bound to a gateway — that binding is
			// authoritative. A missing/erroring target surfaces (a dangling
			// binding is not silently rerouted).
			return r.Ent.GatewayConnection.Get(ctx, *d.GatewayConnectionID)
		}
		// department not found, or no binding → caller decides whether to
		// surface "no gateway for department" as an error.
	}
	return nil, nil
}

// gatewayKeyClient resolves the gateway.Client (key/team ops) for a
// department — its bound gateway, or nil when unbound. nil = no gateway
// configured for this department.
func (r *Resolver) gatewayKeyClient(ctx context.Context, deptID *uuid.UUID) gateway.Client {
	if g, err := r.resolveDeptGateway(ctx, deptID); err == nil && g != nil {
		return r.buildGatewayKeyClient(ctx, g)
	}
	return nil
}

// gatewayKeyClientForConn resolves the gateway.Client by an EXPLICIT
// connection id. nil when the conn is not registered.
func (r *Resolver) gatewayKeyClientForConn(ctx context.Context, connID *uuid.UUID) gateway.Client {
	if connID != nil {
		if g, err := r.Ent.GatewayConnection.Get(ctx, *connID); err == nil {
			return r.buildGatewayKeyClient(ctx, g)
		}
	}
	return nil
}

// modelGatewayClientForVK routes key ops to the gateway that ISSUED the key
// (LLD-14 §3.3): its persisted model_gateway_id (single column that
// carries both the user-facing ModelGateway association and the LLD-14
// post-issue lifecycle pin). This decouples a key's lifecycle
// (revoke/regenerate/recycle/disable) from the department's *current*
// gateway binding.
//
// 2026-07 rename: was gatewayKeyClientForVK. Field rename:
// vk.GatewayConnectionID → vk.ModelGatewayID.
func (r *Resolver) modelGatewayClientForVK(ctx context.Context, vk *ent.VirtualKey) gateway.Client {
	if vk.ModelGatewayID != uuid.Nil {
		connID := vk.ModelGatewayID
		return r.gatewayKeyClientForConn(ctx, &connID)
	}
	return r.gatewayKeyClient(ctx, nil)
}

// ReconcileTargets partitions every governance row across the gateways that
// own it, so the background reconciler scans each gateway against only its
// own keys/teams (LLD-14 §3.4 / OQ-5). Exported so cmd/server can wire it
// as the reconciler's per-cycle GatewaysFunc.
//
// Each GatewayConnection becomes a target. A key is assigned to the gateway
// that ISSUED it (its persisted gateway_connection_id); a legacy NULL key
// (minted before T1) — or one whose recorded gateway no longer exists —
// falls back to the §3.3 department-derived gateway (its team's department
// binding, else the platform default). The platform default still exists in
// this layer for key lifecycle continuity (LLD-14): without it, a legacy
// NULL key has no issuing gateway to scan against, so it is left unscanned
// rather than wrongly revoked.
func (r *Resolver) ReconcileTargets(ctx context.Context) ([]reconcile.GatewayTarget, error) {
	conns, err := r.Ent.GatewayConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	depts, err := r.Ent.Department.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	// No DB gateway configured → nothing to reconcile against.
	if len(conns) == 0 {
		return nil, nil
	}

	// One row bucket per GatewayConnection.
	type bucket struct {
		conn  *ent.GatewayConnection
		keys  []*ent.VirtualKey
		depts []*ent.Department
	}
	buckets := make(map[uuid.UUID]*bucket, len(conns))
	for _, c := range conns {
		buckets[c.ID] = &bucket{conn: c}
	}
	deptByID := make(map[uuid.UUID]*ent.Department, len(depts))
	for _, d := range depts {
		deptByID[d.ID] = d
	}

	// connForDept applies resolveDeptGateway's binding selection to the
	// partition: a department's bound gateway when it still exists. A
	// dangling binding is dropped → the row is left unscanned (rather than
	// silently rerouted).
	connForDept := func(d *ent.Department) *ent.GatewayConnection {
		if d != nil && d.GatewayConnectionID != nil {
			if b, ok := buckets[*d.GatewayConnectionID]; ok {
				return b.conn
			}
		}
		return nil
	}

	// Assign each key to its issuing gateway (persisted id). Per-agent-per-org
	// refactor removed the legacy department-based fallback: every key has a
	// model_gateway_id set on issue. Keys pointing at a missing gateway
	// (deleted conn) are left unscanned — LLD-14 never wrongly revokes.
	for _, vk := range keys {
		var c *ent.GatewayConnection
		if vk.ModelGatewayID != uuid.Nil {
			if b, ok := buckets[vk.ModelGatewayID]; ok {
				c = b.conn
			}
		}
		if c == nil {
			continue
		}
		buckets[c.ID].keys = append(buckets[c.ID].keys, vk)
	}

	// Assign each department to its serving gateway (its binding only —
	// un-bound departments are left unscanned).
	for _, d := range depts {
		if c := connForDept(d); c != nil {
			buckets[c.ID].depts = append(buckets[c.ID].depts, d)
		}
	}

	targets := make([]reconcile.GatewayTarget, 0, len(buckets))
	for _, b := range buckets {
		targets = append(targets, reconcile.GatewayTarget{
			Gateway: r.buildGatewayKeyClient(ctx, b.conn),
			Keys:    b.keys,
			Depts:   b.depts,
		})
	}
	return targets, nil
}

// deptIDFromTeam interprets a virtual key's team id as a department id (a
// key's team == its litellm team == the department, LLD-13 §3.3). Returns
// nil when absent or not a uuid → the caller routes to the department's
// gateway if bound.
func deptIDFromTeam(teamID *string) *uuid.UUID {
	if teamID == nil || *teamID == "" {
		return nil
	}
	if id, err := uuid.Parse(*teamID); err == nil {
		return &id
	}
	return nil
}

// resolveKeyGateway resolves the GatewayConnection that should ISSUE a key
// for a department (its bound gateway) AND a client bound to it. The
// returned connection — nil when unbound — is persisted on the VirtualKey
// row so the key's later lifecycle (revoke/recycle/reconcile) routes back
// to the same gateway.
func (r *Resolver) resolveKeyGateway(ctx context.Context, deptID *uuid.UUID) (*ent.GatewayConnection, gateway.Client) {
	if g, err := r.resolveDeptGateway(ctx, deptID); err == nil && g != nil {
		return g, r.buildGatewayKeyClient(ctx, g)
	}
	return nil, nil
}

// deployGateway resolves the key client and the issuing
// GatewayConnection (LLD-13 §3.3 / LLD-14): the department's gateway, or
// nil when unbound. The VM calls the gateway's endpoint directly — public
// URL = the gateway's endpoint (the LLD-13 §3.3 public_url field was
// removed along with the rest of the legacy GatewayConnection surface).
func (r *Resolver) deployGateway(ctx context.Context, deptID *uuid.UUID) (gateway.Client, *ent.GatewayConnection) {
	if conn, gw := r.resolveKeyGateway(ctx, deptID); conn != nil {
		return gw, conn
	}
	return nil, nil
}

// gatewayModelsForConn builds the ModelManager for a SPECIFIC connection
// (a connection test). Always a real per-row client (the production
// gateway under test).
func (r *Resolver) gatewayModelsForConn(ctx context.Context, g *ent.GatewayConnection) gateway.ModelManager {
	return r.buildGatewayModels(ctx, g)
}

// gateway connection status enum re-exported so callers can refer to it
// without importing the ent package directly. Used by the sync overlay in
// gateway_facade.go.
var _ = gatewayconnection.StatusConnected
