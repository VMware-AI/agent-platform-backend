package graph

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/google/uuid"
)

// StartVirtualKeySpendRefresh was removed in PR #3 cut-over. The unified
// reconciler's spend_refresh phase now drives this loop; the body lives in
// refreshVirtualKeySpendOnce below, which RefreshOneVirtualKeySpend calls
// per-key.

// refreshVirtualKeySpendOnce walks every active (non-revoked) key, calls
// the issuing gateway's /key/info, and updates the platform row with the
// latest spend + last_active_at. Errors are logged and skipped — one bad
// key never blocks the rest of the batch.
func (r *Resolver) refreshVirtualKeySpendOnce(ctx context.Context) {
	keys, err := r.Ent.VirtualKey.Query().
		Where(virtualkey.StatusNEQ(virtualkey.StatusRevoked)).
		All(ctx)
	if err != nil {
		log.Printf("virtual-key spend refresh: query: %v", err)
		return
	}
	for _, k := range keys {
		r.RefreshOneVirtualKeySpend(ctx, k)
	}
}

// RefreshOneVirtualKeySpend issues a single per-key /key/info probe. The
// key's gateway_connection_id drives which gateway to hit (LLD-14 —
// "key's whole lifecycle routes by issuing gateway"); when missing (legacy
// rows predating the field), falls back to the platform default.
//
// Exported so internal/reconcile.Reconciler can call it via the
// ResolverSource interface from the unified cycle's spend_refresh phase.
func (r *Resolver) RefreshOneVirtualKeySpend(ctx context.Context, k *ent.VirtualKey) {
	g, err := r.gatewayForVirtualKey(ctx, k)
	if err != nil || g == nil {
		log.Printf("virtual-key spend refresh: %s no gateway: %v", k.ID, err)
		return
	}
	mk := r.gatewayMasterKey(ctx, g)
	if mk == "" {
		log.Printf("virtual-key spend refresh: gateway %s has no master key", g.Name)
		return
	}
	http, err := gateway.NewHTTPClient(g.Endpoint, mk)
	if err != nil {
		log.Printf("virtual-key spend refresh: build client %s: %v", g.Name, err)
		return
	}
	// /key/info takes the raw key string (litellm_key, which is the secret
	// issued at creation). We never log this on error — the gateway client's
	// own redactSecrets already strips `sk-` tokens from any 4xx body.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := http.BudgetInfo(probeCtx, gateway.BudgetScopeKey, k.LitellmKey)
	if err != nil {
		// 404 from /key/info means the LiteLLM-side key is gone — typically
		// because the reconciler's Drift C branch (DB row revoked + gw
		// still had it) DELETEd it on a prior cycle, or an operator
		// removed it out-of-band. Either way the spend read has nothing
		// to update against; skip silently so we don't spam WARN every
		// 15min. The keys phase's diff already detects Drift B / Drift C
		// and reacts there — spend refresh is purely telemetry.
		if errors.Is(err, gateway.ErrNotFound) {
			return
		}
		log.Printf("virtual-key spend refresh: probe %s: %v", k.ID, err)
		return
	}
	if info == nil {
		return
	}
	// Convert spend to int (ent stores it as int per PR1.3 schema).
	newSpend := int(info.Spend)
	// Compare to current — only write when something changed, to avoid
	// the hot-write path a periodic refresh always trips otherwise.
	if k.Spend == newSpend && info.LastActiveAt == nil && k.LastActiveAt == nil {
		return
	}
	upd := r.Ent.VirtualKey.UpdateOneID(k.ID).SetSpend(newSpend)
	if info.LastActiveAt != nil {
		if t, parseErr := time.Parse(time.RFC3339, *info.LastActiveAt); parseErr == nil {
			upd.SetLastActiveAt(t)
		}
		// If parse fails, skip the last_active_at write — keep the
		// existing value rather than clobber with nil. Logged for ops to
		// investigate if it's a recurring pattern.
	}
	if _, err := upd.Save(ctx); err != nil {
		log.Printf("virtual-key spend refresh: persist %s: %v", k.ID, err)
	}
}

// gatewayForVirtualKey returns the GatewayConnection that should serve the
// per-key /key/info call. Routing priority: key.gateway_connection_id
// (LLD-14 — issuing gateway). Returns (nil, nil) when the issuing gateway
// was deleted or never set — caller treats as a no-op for that tick.
// (The platform-default fallback has been retired; per-agent-per-org
// refactor (2026-07) requires every key to have model_gateway_id set.)
func (r *Resolver) gatewayForVirtualKey(ctx context.Context, k *ent.VirtualKey) (*ent.GatewayConnection, error) {
	if k.ModelGatewayID != uuid.Nil {
		g, err := r.Ent.GatewayConnection.Get(ctx, k.ModelGatewayID)
		if err == nil {
			return g, nil
		}
		if !ent.IsNotFound(err) {
			return nil, err
		}
		// FK row missing (deleted gateway) — leave the key unrefreshed for
		// this tick; a re-run with the gateway restored will pick it up.
	}
	return nil, nil
}
