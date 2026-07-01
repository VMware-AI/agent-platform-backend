package graph

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/sony/gobreaker"
)

// syncOnePool is the single shared entry point for "sync one pool". It is
// reached from:
//   - CreateResourcePool fire-and-forget first sync
//   - the background ticker (StartAutoSync → syncAllPools)
//   - the manual syncResourcePool mutation
//
// All three paths get the same fault-tolerance chain:
//
//	30s per-pool timeout  →  exponential-backoff retries (≤ maxRetries)
//	                     →  sony/gobreaker per endpoint (open state skips the
//	                        whole chain and leaves the existing pool status
//	                        alone — important when many pools share an
//	                        endpoint and one vCenter is briefly down).
//
// On real failure → status=error (so the operator can spot the outage).
// On breaker open → log + leave status untouched, the next ticker tick
// will try the half-open probe.
func (r *Resolver) syncOnePool(ctx context.Context, pool *ent.ResourcePool) (*ent.ResourcePool, time.Time, error) {
	// When sync plumbing is disabled (e.g. most unit tests), still run the
	// core connect→inventory→full-inventory pipeline — just skip the breaker
	// + retry layers. The mutations (SyncResourcePool) and the fire-and-
	// forget first sync behave the same in both modes.
	//
	// Default timeout is 30s when callers haven't called EnablePoolSync.
	// A zero-or-negative value here would otherwise make context.WithTimeout
	// fire an immediate deadline (a context.WithTimeout(ctx, 0) is already
	// expired), which is a foot-gun we guard against.
	timeout := r.poolSyncTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	syncCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := func() error {
		return retrySync(syncCtx, r.poolSyncMaxRetries, func(ctx context.Context) error {
			conn, err := r.connectPool(ctx, pool)
			if err != nil {
				return vcenter.MaybeRetryable(err)
			}
			defer func() { _ = conn.Logout(ctx) }()

			if _, err := conn.Inventory(ctx); err != nil {
				return vcenter.MaybeRetryable(err)
			}
			inventory, err := conn.FullInventory(ctx)
			if err != nil {
				return vcenter.MaybeRetryable(err)
			}

			now := time.Now()
			updated, err := r.Ent.ResourcePool.UpdateOne(pool).
				SetStatus(resourcepool.StatusConnected).
				SetLastSyncedAt(now).
				SetInventory(inventory).
				Save(ctx)
			if err != nil {
				return err
			}
			pool = updated
			return nil
		})
	}

	var err error
	if r.poolBreakers == nil {
		err = run()
	} else {
		cb := r.poolBreakers.get(pool.Endpoint)
		_, err = cb.Execute(func() (any, error) {
			return nil, run()
		})
	}

	if err != nil {
		if r.poolBreakers != nil {
			if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
				log.Printf("pool-sync %s: breaker open, skip (status left as %s)", pool.Name, pool.Status)
				return nil, time.Time{}, err
			}
		}
		_, _ = r.Ent.ResourcePool.UpdateOne(pool).
			SetStatus(resourcepool.StatusError).
			Save(context.Background())
		return nil, time.Time{}, err
	}

	return pool, time.Now().UTC(), nil
}