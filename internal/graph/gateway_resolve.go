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
		if cred, err := r.Secrets.Resolve(ctx, g.MasterKeyRef); err == nil {
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

// defaultGateway returns the platform default GatewayConnection (is_default), or
// (nil, nil) when none is marked — callers then fall back to the legacy injected
// r.Gateway/r.GatewayModels (test + transition seam).
func (r *Resolver) defaultGateway(ctx context.Context) (*ent.GatewayConnection, error) {
	g, err := r.Ent.GatewayConnection.Query().
		Where(gatewayconnection.IsDefault(true)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return g, err
}

// resolveDeptGateway picks the GatewayConnection that should serve a department's
// litellm ops (LLD-13 §3.3): the department's own gateway_connection_id if set,
// else the platform default. Returns (nil, nil) when neither resolves.
func (r *Resolver) resolveDeptGateway(ctx context.Context, deptID *uuid.UUID) (*ent.GatewayConnection, error) {
	if deptID != nil {
		d, err := r.Ent.Department.Get(ctx, *deptID)
		switch {
		case err != nil && !ent.IsNotFound(err):
			return nil, err // a real DB error must surface, not silently route to default
		case err == nil && d.GatewayConnectionID != nil:
			// The department is explicitly bound to a gateway — that binding is
			// authoritative. A missing/erroring target surfaces (a dangling binding is
			// not silently rerouted to the default).
			return r.Ent.GatewayConnection.Get(ctx, *d.GatewayConnectionID)
		}
		// department not found, or no binding → fall through to the platform default.
	}
	return r.defaultGateway(ctx)
}

// gatewayKeyClient resolves the gateway.Client (key/team ops) for a department —
// its bound gateway, else the default — building a per-connection client. Falls
// back to the legacy injected r.Gateway when no DB gateway is configured (keeps
// existing tests + a not-yet-migrated install working). nil = no gateway at all.
func (r *Resolver) gatewayKeyClient(ctx context.Context, deptID *uuid.UUID) gateway.Client {
	if g, err := r.resolveDeptGateway(ctx, deptID); err == nil && g != nil {
		return r.buildGatewayKeyClient(ctx, g)
	}
	return r.Gateway
}

// gatewayKeyClientForConn resolves the gateway.Client by an EXPLICIT connection
// id (createDepartment picks the gateway directly), else the platform default,
// else the legacy injected r.Gateway. nil = no gateway configured.
func (r *Resolver) gatewayKeyClientForConn(ctx context.Context, connID *uuid.UUID) gateway.Client {
	if connID != nil {
		if g, err := r.Ent.GatewayConnection.Get(ctx, *connID); err == nil {
			return r.buildGatewayKeyClient(ctx, g)
		}
	}
	if g, err := r.defaultGateway(ctx); err == nil && g != nil {
		return r.buildGatewayKeyClient(ctx, g)
	}
	return r.Gateway
}

// gatewayKeyClientForVK routes key ops to the gateway that ISSUED the key (LLD-14
// §3.3): its persisted gateway_connection_id, else — for legacy rows minted before
// T1 (NULL) — the team_id→department→gateway derivation. This decouples a key's
// lifecycle (revoke/regenerate/recycle/disable) from the department's *current*
// gateway binding, so a department re-bind can't strand the key on its original
// gateway as an active billable orphan (bug #5).
func (r *Resolver) gatewayKeyClientForVK(ctx context.Context, vk *ent.VirtualKey) gateway.Client {
	if vk.GatewayConnectionID != nil {
		return r.gatewayKeyClientForConn(ctx, vk.GatewayConnectionID)
	}
	return r.gatewayKeyClient(ctx, deptIDFromTeam(&vk.TeamID))
}

// gatewayModels resolves the gateway.ModelManager (upstream/router sync) — the
// platform default gateway — falling back to the legacy injected r.GatewayModels.
// nil = no gateway configured.
func (r *Resolver) gatewayModels(ctx context.Context) gateway.ModelManager {
	// Legacy injected fake (old gateway-routing tests) wins only when no
	// per-connection builder is injected; otherwise resolve the default gateway.
	if r.GatewayClientFor == nil && r.GatewayModels != nil {
		return r.GatewayModels
	}
	if g, err := r.defaultGateway(ctx); err == nil && g != nil {
		return r.buildGatewayModels(ctx, g)
	}
	return r.GatewayModels
}

// ReconcileTargets partitions every governance row across the gateways that own
// it, so the background reconciler scans each gateway against only its own keys/
// teams (LLD-14 §3.4 / OQ-5). Exported so cmd/server can wire it as the
// reconciler's per-cycle GatewaysFunc.
//
// Each GatewayConnection becomes a target. A key is assigned to the gateway that
// ISSUED it (its persisted gateway_connection_id); a legacy NULL key (minted
// before T1) — or one whose recorded gateway no longer exists — falls back to the
// §3.3 department-derived gateway (its team's department binding, else the
// default). Departments are assigned by the same binding-else-default rule. Rows
// that resolve to no gateway at all (no default configured) are left unscanned —
// never wrongly revoked. When no GatewayConnection is configured, a single legacy
// target (the injected r.Gateway + all rows) preserves the pre-LLD-13
// single-gateway install; nil when there is no gateway at all (caller skips).
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

	// No DB gateway configured → legacy single-gateway install: reconcile the
	// injected r.Gateway against all rows (pre-LLD-13 behavior). nil when there is
	// no gateway at all → the caller skips the cycle.
	if len(conns) == 0 {
		if r.Gateway == nil {
			return nil, nil
		}
		return []reconcile.GatewayTarget{{Gateway: r.Gateway, Keys: keys, Depts: depts}}, nil
	}

	// One row bucket per GatewayConnection, plus a lookup of the platform default.
	type bucket struct {
		conn  *ent.GatewayConnection
		keys  []*ent.VirtualKey
		depts []*ent.Department
	}
	buckets := make(map[uuid.UUID]*bucket, len(conns))
	var defaultConn *ent.GatewayConnection
	for _, c := range conns {
		buckets[c.ID] = &bucket{conn: c}
		if c.IsDefault {
			defaultConn = c
		}
	}
	deptByID := make(map[uuid.UUID]*ent.Department, len(depts))
	for _, d := range depts {
		deptByID[d.ID] = d
	}

	// connForDept applies resolveDeptGateway's binding-else-default selection to the
	// partition: a department's bound gateway when it still exists, else the platform
	// default (nil when neither). Unlike resolveDeptGateway it does NOT surface a
	// dangling binding as an error — for a bounded, best-effort background scan,
	// routing such a (structurally-prevented) row to the default bucket is safe.
	connForDept := func(d *ent.Department) *ent.GatewayConnection {
		if d != nil && d.GatewayConnectionID != nil {
			if b, ok := buckets[*d.GatewayConnectionID]; ok {
				return b.conn
			}
		}
		return defaultConn
	}

	// Assign each key to its issuing gateway (persisted id), else the §3.3
	// department-derived fallback for legacy NULL / dangling rows.
	for _, vk := range keys {
		var c *ent.GatewayConnection
		if vk.GatewayConnectionID != nil {
			if b, ok := buckets[*vk.GatewayConnectionID]; ok {
				c = b.conn
			}
		}
		if c == nil {
			var dept *ent.Department
			if deptID := deptIDFromTeam(&vk.TeamID); deptID != nil {
				dept = deptByID[*deptID]
			}
			c = connForDept(dept)
		}
		if c == nil {
			continue // unresolved + no default → leave unscanned (never wrongly revoked)
		}
		buckets[c.ID].keys = append(buckets[c.ID].keys, vk)
	}

	// Assign each department to its serving gateway (binding else default).
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

// deptIDFromTeam interprets a virtual key's team id as a department id (a key's
// team == its litellm team == the department, LLD-13 §3.3). Returns nil when
// absent or not a uuid → the caller routes to the platform default gateway.
func deptIDFromTeam(teamID *string) *uuid.UUID {
	if teamID == nil || *teamID == "" {
		return nil
	}
	if id, err := uuid.Parse(*teamID); err == nil {
		return &id
	}
	return nil
}

// resolveKeyGateway resolves the GatewayConnection that should ISSUE a key for a
// department (its bound gateway, else the platform default) AND a client bound to
// it (LLD-14 §3.2). The returned connection — nil when only the legacy injected
// r.Gateway is available — is persisted on the VirtualKey row so the key's later
// lifecycle (revoke/recycle/reconcile) routes back to the same gateway, decoupled
// from the department's *current* binding.
func (r *Resolver) resolveKeyGateway(ctx context.Context, deptID *uuid.UUID) (*ent.GatewayConnection, gateway.Client) {
	if g, err := r.resolveDeptGateway(ctx, deptID); err == nil && g != nil {
		return g, r.buildGatewayKeyClient(ctx, g)
	}
	return nil, r.Gateway
}

// deployGateway resolves the key client, the public URL a deployed agent's VM
// should call, AND the issuing GatewayConnection (LLD-13 §3.3 / LLD-14): the
// department's gateway, else the platform default, else the legacy injected
// r.Gateway + r.GatewayURL. A nil client = unconfigured; a nil connection = the
// legacy fallback (no DB row to persist on the key).
func (r *Resolver) deployGateway(ctx context.Context, deptID *uuid.UUID) (gateway.Client, string, *ent.GatewayConnection) {
	if conn, gw := r.resolveKeyGateway(ctx, deptID); conn != nil {
		return gw, gatewayPublicURL(conn), conn
	}
	return r.Gateway, r.GatewayURL, nil
}

// gatewayModelsForConn builds the ModelManager for a SPECIFIC connection (a
// connection test). The legacy injected fake (old gateway-routing tests) wins only
// when no per-connection builder is injected; otherwise a real per-row client is
// built — so production tests the gateway under test, not a process-wide default.
func (r *Resolver) gatewayModelsForConn(ctx context.Context, g *ent.GatewayConnection) gateway.ModelManager {
	if r.GatewayClientFor == nil && r.GatewayModels != nil {
		return r.GatewayModels
	}
	return r.buildGatewayModels(ctx, g)
}

// gatewayPublicURL is the URL provisioned VMs/agents call: the connection's
// public_url, or its endpoint when unset (LLD-13 §3.3, replaces GATEWAY_PUBLIC_URL).
func gatewayPublicURL(g *ent.GatewayConnection) string {
	if g.PublicURL != "" {
		return g.PublicURL
	}
	return g.Endpoint
}
