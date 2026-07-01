package graph

import (
	"context"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
)

// StartAutoSync periodically re-syncs every resource pool that has a secret
// reference (i.e. credentials are stored). It runs until ctx is cancelled.
// A single cycle that errors on one pool logs and continues with the rest —
// a bad pool never blocks the others. The actual sync work is delegated to
// syncOnePool so the fire-and-forget first sync (CreateResourcePool) and the
// manual syncResourcePool mutation share the same fault-tolerance chain
// (timeout → retry → breaker).
func (r *Resolver) StartAutoSync(ctx context.Context, interval time.Duration) {
	log.Printf("pool auto-sync: every %s", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.syncAllPools(ctx)
		}
	}
}

func (r *Resolver) syncAllPools(ctx context.Context) {
	if r.VCenterConnect == nil {
		return
	}
	tickCtx := withSyncSource(ctx, syncSourceTicker)
	pools, err := r.Ent.ResourcePool.Query().
		Where(resourcepool.SecretRefNEQ("")).
		All(ctx)
	if err != nil {
		log.Printf("pool auto-sync: query pools: %v", err)
		return
	}
	log.Printf("pool auto-sync: ticking %d pool(s) with stored credentials", len(pools))
	for _, pool := range pools {
		if _, _, err := r.syncOnePool(tickCtx, pool); err != nil {
			// syncOnePool already stamps status=error on real failures and
			// suppresses the stamp when the breaker is open. Just log here
			// so we keep ticker progress visible.
			log.Printf("pool auto-sync: pool %s: %v", pool.Name, err)
		}
	}
}