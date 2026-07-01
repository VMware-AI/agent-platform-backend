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

	// Identify which entry point invoked us, so log lines tell the operator
	// whether a given sync came from the fire-and-forget first sync, the
	// background ticker, or the manual mutation. The caller tags the ctx
	// via syncSourceTag; default "unknown" if absent.
	source := syncSourceFromCtx(ctx)
	poolID := pool.ID.String()
	poolName := pool.Name

	log.Printf("pool-sync [%s] start pool=%s id=%s endpoint=%s timeout=%s secret_ref=%q",
		source, poolName, poolID, pool.Endpoint, timeout,
		secretRefDisplay(pool.SecretRef))

	// runWithRetry wraps retrySync so we can log each attempt's outcome
	// (attempt number + retryable classification + elapsed).
	run := func() error {
		attempts := 0
		return retrySync(syncCtx, r.poolSyncMaxRetries, func(ctx context.Context) error {
			attempts++
			attemptStart := time.Now()
			log.Printf("pool-sync [%s] attempt %d/%d pool=%s: connect start",
				source, attempts, r.poolSyncMaxRetries+1, poolName)

			conn, err := r.connectPool(ctx, pool)
			if err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: connect failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(attemptStart))
				return vcenter.MaybeRetryable(err)
			}
			defer func() { _ = conn.Logout(ctx) }()

			log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() start", source, attempts, poolName)
			if _, err := conn.Inventory(ctx); err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(attemptStart))
				return vcenter.MaybeRetryable(err)
			}
			log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() ok elapsed=%s",
				source, attempts, poolName, time.Since(attemptStart))

			log.Printf("pool-sync [%s] attempt %d pool=%s: FullInventory() start", source, attempts, poolName)
			fullStart := time.Now()
			inventory, err := conn.FullInventory(ctx)
			if err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: FullInventory() failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(fullStart))
				return vcenter.MaybeRetryable(err)
			}
			storagePolicyStatus := "absent"
			for _, dc := range inventory {
				if dc.StoragePolicies != nil {
					if len(dc.StoragePolicies) == 0 {
						storagePolicyStatus = "empty"
					} else {
						storagePolicyStatus = "ok"
					}
					break
				}
			}
			log.Printf("pool-sync [%s] attempt %d pool=%s: FullInventory() ok dcs=%d elapsed=%s storage_policies=%s",
				source, attempts, poolName, len(inventory), time.Since(fullStart), storagePolicyStatus)

			log.Printf("pool-sync [%s] attempt %d pool=%s: persist start", source, attempts, poolName)
			persistStart := time.Now()
			now := time.Now()
			updated, err := r.Ent.ResourcePool.UpdateOne(pool).
				SetStatus(resourcepool.StatusConnected).
				SetLastSyncedAt(now).
				SetInventory(inventory).
				Save(ctx)
			if err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: persist failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(persistStart))
				return err
			}
			log.Printf("pool-sync [%s] attempt %d pool=%s: persist ok last_synced_at=%s elapsed=%s",
				source, attempts, poolName, now.UTC().Format(time.RFC3339), time.Since(persistStart))
			pool = updated
			return nil
		})
	}

	start := time.Now()
	var err error
	if r.poolBreakers == nil {
		log.Printf("pool-sync [%s] pool=%s: breaker disabled (no EnablePoolSync), running raw",
			source, poolName)
		err = run()
	} else {
		cb := r.poolBreakers.get(pool.Endpoint)
		log.Printf("pool-sync [%s] pool=%s: breaker execute start (endpoint=%s)",
			source, poolName, pool.Endpoint)
		_, err = cb.Execute(func() (any, error) {
			return nil, run()
		})
	}

	if err != nil {
		if r.poolBreakers != nil {
			if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
				log.Printf("pool-sync [%s] pool=%s: breaker open, skipped (status left as %s) total_elapsed=%s",
					source, poolName, pool.Status, time.Since(start))
				return nil, time.Time{}, err
			}
		}
		log.Printf("pool-sync [%s] pool=%s: FAILED err=%q total_elapsed=%s -> status=error",
			source, poolName, err, time.Since(start))
		_, _ = r.Ent.ResourcePool.UpdateOne(pool).
			SetStatus(resourcepool.StatusError).
			Save(context.Background())
		return nil, time.Time{}, err
	}

	log.Printf("pool-sync [%s] pool=%s: OK total_elapsed=%s last_synced_at=%s",
		source, poolName, time.Since(start),
		pool.LastSyncedAt.UTC().Format(time.RFC3339))
	return pool, pool.LastSyncedAt.UTC(), nil
}