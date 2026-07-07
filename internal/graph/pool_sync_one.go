package graph

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
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
func (r *Resolver) syncOnePool(ctx context.Context, pool *ent.ResourcePool) (synced *ent.ResourcePool, syncedAt time.Time, err error) {
	// Identify which entry point invoked us, so log lines tell the operator
	// whether a given sync came from the fire-and-forget first sync, the
	// background ticker, or the manual mutation. The caller tags the ctx
	// via syncSourceTag; default "unknown" if absent.
	source := syncSourceFromCtx(ctx)
	poolID := pool.ID.String()
	poolName := pool.Name

	// Serialise concurrent syncs of the SAME pool (#98 item 5b): the ticker, the
	// manual mutation and the create-time first sync can all target one pool at
	// once. Without this their status + inventory writes interleave (last-writer-
	// wins) and the vCenter login load doubles. Different pools still sync in
	// parallel — the lock is per pool id.
	lock := r.poolSyncLock(pool.ID)
	lock.Lock()
	defer lock.Unlock()

	// Recover any panic on the sync path (#98 item 5c). syncOnePool runs under a
	// bare goroutine for the first sync and under the ticker goroutine — neither
	// is wrapped by net/http's per-request recover — so an unrecovered panic on
	// the vCenter/ent path would crash the whole control plane. Log the stack,
	// mark the pool status=error, and return a non-nil error so callers observe
	// the failure instead of a silent nil.
	defer func() {
		if p := recover(); p != nil {
			log.Printf("pool-sync [%s] pool=%s: PANIC recovered: %v\n%s", source, poolName, p, debug.Stack())
			if _, werr := r.Ent.ResourcePool.UpdateOne(pool).
				SetStatus(resourcepool.StatusError).
				Save(context.Background()); werr != nil {
				log.Printf("pool-sync [%s] pool=%s: status=error write after panic failed: %v", source, poolName, werr)
			}
			synced, syncedAt, err = nil, time.Time{}, fmt.Errorf("pool sync panicked: %v", p)
		}
	}()

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
				return err
			}
			defer func() { _ = conn.Logout(ctx) }()

			log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() start", source, attempts, poolName)
			if _, err := conn.Inventory(ctx); err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(attemptStart))
				return err
			}
			log.Printf("pool-sync [%s] attempt %d pool=%s: inventory() ok elapsed=%s",
				source, attempts, poolName, time.Since(attemptStart))

			log.Printf("pool-sync [%s] attempt %d pool=%s: FullInventory() start", source, attempts, poolName)
			fullStart := time.Now()
			inventory, err := conn.FullInventory(ctx)
			if err != nil {
				log.Printf("pool-sync [%s] attempt %d pool=%s: FullInventory() failed err=%q elapsed=%s",
					source, attempts, poolName, err, time.Since(fullStart))
				return err
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
				// Tag the local DB write failure so the breaker layer can tell it
				// apart from a genuine vCenter fault and NOT trip the vCenter breaker
				// on it (#98 item 5d). It is also non-retryable (not a transport
				// error), so retrySync returns it immediately.
				return &poolPersistError{err: err}
			}
			log.Printf("pool-sync [%s] attempt %d pool=%s: persist ok last_synced_at=%s elapsed=%s",
				source, attempts, poolName, now.UTC().Format(time.RFC3339), time.Since(persistStart))
			pool = updated
			return nil
		})
	}

	start := time.Now()
	// err is the named return; the recover defer above may also set it. Here it
	// carries the sync outcome for the status write below.
	if r.poolBreakers == nil {
		log.Printf("pool-sync [%s] pool=%s: breaker disabled (no EnablePoolSync), running raw",
			source, poolName)
		err = run()
	} else {
		cb := r.poolBreakers.get(pool.Endpoint)
		log.Printf("pool-sync [%s] pool=%s: breaker execute start (endpoint=%s)",
			source, poolName, pool.Endpoint)
		// Only genuine vCenter dial/API failures should feed the vCenter breaker
		// (#98 item 5d). A local DB write failure (poolPersistError) means the
		// vCenter side actually succeeded — connect + full inventory returned — so
		// we hide it from the breaker (return nil to Execute → counts as success)
		// but still surface the real error to the caller via persistErr for the
		// status=error write below.
		var persistErr error
		_, err = cb.Execute(func() (any, error) {
			runErr := run()
			var pe *poolPersistError
			if errors.As(runErr, &pe) {
				persistErr = runErr
				return nil, nil // breaker sees success; vCenter was reachable
			}
			return nil, runErr
		})
		if err == nil && persistErr != nil {
			err = persistErr
		}
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
		// Best-effort status write, but never silently: if even this errors the
		// row keeps its stale status and the operator should know why (#98).
		if _, werr := r.Ent.ResourcePool.UpdateOne(pool).
			SetStatus(resourcepool.StatusError).
			Save(context.Background()); werr != nil {
			log.Printf("pool-sync [%s] pool=%s: status=error write failed: %v", source, poolName, werr)
		}
		return nil, time.Time{}, err
	}

	log.Printf("pool-sync [%s] pool=%s: OK total_elapsed=%s last_synced_at=%s",
		source, poolName, time.Since(start),
		pool.LastSyncedAt.UTC().Format(time.RFC3339))
	return pool, pool.LastSyncedAt.UTC(), nil
}

// poolPersistError wraps a failure of the LOCAL DB write that stamps a pool's
// synced inventory. It is distinguished from a vCenter error so the breaker layer
// does not trip the per-endpoint vCenter circuit breaker on a database problem
// (the vCenter round-trip already succeeded) — see syncOnePool (#98 item 5d).
type poolPersistError struct{ err error }

func (e *poolPersistError) Error() string { return "pool persist: " + e.err.Error() }
func (e *poolPersistError) Unwrap() error { return e.err }

// poolSyncLock returns the per-pool mutex for id, creating it on first use. The
// registry itself is guarded by poolSyncLocksMu; the returned lock serialises
// syncs of that one pool (#98 item 5b). Entries are never evicted — one *Mutex
// per pool is negligible and avoids a lock-while-held eviction race.
func (r *Resolver) poolSyncLock(id uuid.UUID) *sync.Mutex {
	r.poolSyncLocksMu.Lock()
	defer r.poolSyncLocksMu.Unlock()
	if r.poolSyncLocks == nil {
		r.poolSyncLocks = make(map[uuid.UUID]*sync.Mutex)
	}
	lock, ok := r.poolSyncLocks[id]
	if !ok {
		lock = &sync.Mutex{}
		r.poolSyncLocks[id] = lock
	}
	return lock
}
