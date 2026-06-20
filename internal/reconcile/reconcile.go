// Package reconcile detects and (optionally) heals drift between the platform's
// virtual-key governance rows and the keys the LiteLLM gateway actually holds.
//
// Drift arises when a mutation half-completes: e.g. IssueVirtualKey mints a key
// at the gateway but the governing DB row fails to persist AND the compensating
// revoke also fails (virtualkey.resolvers.go) — leaving an ungoverned key. Such a
// key bypasses budget/rate-limit governance and is a security risk.
//
// The reconciler is report-only by default ("对账": find discrepancies, change
// nothing). Healing is opt-in via Prune so that a partial gateway listing or an
// identifier mismatch can never trigger accidental mass deletion.
package reconcile

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// KeyGateway is the slice of the gateway client the reconciler depends on.
type KeyGateway interface {
	ListKeys(ctx context.Context) ([]gateway.KeyInfo, error)
	DeleteKey(ctx context.Context, key string) error
}

// Reconciler compares gateway keys against governance rows.
type Reconciler struct {
	Ent     *ent.Client
	Gateway KeyGateway
	// Prune enables healing: delete gateway orphans + revoke stale DB rows. When
	// false (default) the reconciler only reports.
	Prune bool
}

// Report summarizes one reconciliation pass. Key identifiers are kept here for
// the caller but never written to logs (they are secrets/sensitive).
type Report struct {
	GatewayKeys    int      // keys the gateway reported
	DBKeys         int      // governance rows in the DB
	GatewayOrphans []string // at gateway, no DB row (ungoverned)
	StaleRows      []string // DB ids: non-revoked rows whose key vanished at gateway
	Pruned         int      // orphans deleted at the gateway (Prune only)
	Revoked        int      // stale rows marked revoked (Prune only)
}

// ReconcileKeys runs one pass. It returns an error only when it cannot read both
// sides; per-item heal failures are logged and counted, not fatal.
func (r *Reconciler) ReconcileKeys(ctx context.Context) (*Report, error) {
	gwKeys, err := r.Gateway.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gateway keys: %w", err)
	}
	rows, err := r.Ent.VirtualKey.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list governance rows: %w", err)
	}

	dbKeys := make(map[string]struct{}, len(rows))
	for _, vk := range rows {
		dbKeys[vk.LitellmKey] = struct{}{}
	}
	gwKeySet := make(map[string]struct{}, len(gwKeys))

	rep := &Report{GatewayKeys: len(gwKeys), DBKeys: len(rows)}
	for _, k := range gwKeys {
		gwKeySet[k.Key] = struct{}{}
		if _, governed := dbKeys[k.Key]; !governed {
			rep.GatewayOrphans = append(rep.GatewayOrphans, k.Key)
		}
	}
	for _, vk := range rows {
		if vk.Status == virtualkey.StatusRevoked {
			continue // already terminal — its key is expected to be gone
		}
		if _, present := gwKeySet[vk.LitellmKey]; !present {
			rep.StaleRows = append(rep.StaleRows, vk.ID.String())
		}
	}

	if r.Prune {
		r.heal(ctx, rep)
	}

	// Log counts only — never the key identifiers themselves.
	log.Printf("reconcile keys: gateway=%d db=%d orphans=%d stale=%d pruned=%d revoked=%d prune=%v",
		rep.GatewayKeys, rep.DBKeys, len(rep.GatewayOrphans), len(rep.StaleRows),
		rep.Pruned, rep.Revoked, r.Prune)
	return rep, nil
}

// heal deletes gateway orphans and revokes stale rows, counting successes.
func (r *Reconciler) heal(ctx context.Context, rep *Report) {
	for _, key := range rep.GatewayOrphans {
		if err := r.Gateway.DeleteKey(ctx, key); err != nil {
			log.Printf("reconcile: prune gateway orphan failed: %v", err)
			continue
		}
		rep.Pruned++
	}
	for _, id := range rep.StaleRows {
		kid, err := uuid.Parse(id)
		if err != nil {
			log.Printf("reconcile: bad stale row id %q: %v", id, err)
			continue
		}
		if _, err := r.Ent.VirtualKey.UpdateOneID(kid).
			SetStatus(virtualkey.StatusRevoked).Save(ctx); err != nil {
			log.Printf("reconcile: revoke stale row %s failed: %v", id, err)
			continue
		}
		rep.Revoked++
	}
}

// Run reconciles immediately, then every interval until ctx is canceled. Cycle
// errors are logged, not fatal — the loop keeps running.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := r.ReconcileKeys(ctx); err != nil {
			log.Printf("reconcile cycle error: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
