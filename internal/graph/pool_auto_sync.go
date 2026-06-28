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
// a bad pool never blocks the others.
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
	pools, err := r.Ent.ResourcePool.Query().
		Where(resourcepool.SecretRefNEQ("")).
		All(ctx)
	if err != nil {
		log.Printf("pool auto-sync: query pools: %v", err)
		return
	}
	for _, pool := range pools {
		conn, err := r.connectPool(ctx, pool)
		if err != nil {
			log.Printf("pool auto-sync: connect pool %s: %v", pool.Name, err)
			_, _ = r.Ent.ResourcePool.UpdateOne(pool).SetStatus(resourcepool.StatusError).Save(ctx)
			continue
		}
		inv, err := conn.Inventory(ctx)
		_ = conn.Logout(ctx)
		if err != nil {
			log.Printf("pool auto-sync: inventory pool %s: %v", pool.Name, err)
			_, _ = r.Ent.ResourcePool.UpdateOne(pool).SetStatus(resourcepool.StatusError).Save(ctx)
			continue
		}
		_, err = r.Ent.ResourcePool.UpdateOne(pool).
			SetStatus(resourcepool.StatusConnected).
			SetDatacenterCount(inv.Datacenters).
			SetClusterCount(inv.Clusters).
			SetHostCount(inv.Hosts).
			SetVMCount(inv.VMs).
			SetLastSyncedAt(time.Now()).
			Save(ctx)
		if err != nil {
			log.Printf("pool auto-sync: save pool %s: %v", pool.Name, err)
		}
	}
}
