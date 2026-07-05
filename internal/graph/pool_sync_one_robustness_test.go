package graph

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/resourcepool"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// panicVCenter embeds the shared fake but panics inside Inventory, standing in
// for a bug on the vCenter/ent path. Used to prove syncOnePool's recover (#98 5c).
type panicVCenter struct{ fakeVCenter }

func (p *panicVCenter) Inventory(context.Context) (vcenter.Inventory, error) {
	panic("boom in inventory")
}

// countingVCenter embeds the shared fake and records the max number of Inventory
// calls in flight concurrently plus the total. Used to prove the per-pool mutex
// (#98 5b) and that the vCenter breaker is actually reached (#98 5d).
type countingVCenter struct {
	fakeVCenter
	inFlight int32
	maxSeen  int32
	total    int32
	hold     time.Duration
}

func (c *countingVCenter) Inventory(context.Context) (vcenter.Inventory, error) {
	n := atomic.AddInt32(&c.inFlight, 1)
	for {
		m := atomic.LoadInt32(&c.maxSeen)
		if n <= m || atomic.CompareAndSwapInt32(&c.maxSeen, m, n) {
			break
		}
	}
	atomic.AddInt32(&c.total, 1)
	if c.hold > 0 {
		time.Sleep(c.hold)
	}
	atomic.AddInt32(&c.inFlight, -1)
	return vcenter.Inventory{}, nil
}

func poolSyncTestResolver(t *testing.T, connect func() VCenterClient) *Resolver {
	t.Helper()
	r, cleanup := newTestResolver(t)
	t.Cleanup(cleanup)
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.VCenterConnect = func(context.Context, string, string, string, bool) (VCenterClient, error) {
		return connect(), nil
	}
	return r
}

// #98 item 5c: a panic on the sync path must be recovered — syncOnePool returns
// an error (not a crash) and stamps status=error.
func TestSyncOnePool_RecoversPanic(t *testing.T) {
	r := poolSyncTestResolver(t, func() VCenterClient { return &panicVCenter{} })
	pool := r.Ent.ResourcePool.Create().
		SetName("panic-pool").
		SetEndpoint("https://vc-panic").
		SetSecretRef("vault://oc").
		SaveX(context.Background())

	_, _, err := r.syncOnePool(context.Background(), pool)
	if err == nil {
		t.Fatal("expected an error from the recovered panic, got nil")
	}
	got := r.Ent.ResourcePool.GetX(context.Background(), pool.ID)
	if got.Status != resourcepool.StatusError {
		t.Fatalf("panic path should stamp status=error, got %s", got.Status)
	}
}

// #98 item 5d: a local DB persist failure must NOT trip the vCenter circuit
// breaker (the vCenter round-trip succeeded). With threshold=1 a genuine vCenter
// failure would open the breaker after one hit; a persist failure must not — a
// later sync on the same endpoint still reaches vCenter instead of fast-failing.
func TestSyncOnePool_PersistErrorDoesNotTripBreaker(t *testing.T) {
	vc := &countingVCenter{}
	r := poolSyncTestResolver(t, func() VCenterClient { return vc })
	// threshold=1: a single breaker failure would open it immediately.
	r.EnablePoolSync(30*time.Second, 0, 1, 60)

	// Pool A shares the endpoint. Delete its row BEFORE sync so connect+inventory
	// succeed (fake) but the persist UpdateOne(pool) fails with not-found → a
	// poolPersistError, which must be classified as a DB error, not vCenter.
	const endpoint = "https://vc-shared"
	poolA := r.Ent.ResourcePool.Create().
		SetName("pool-a").SetEndpoint(endpoint).SetSecretRef("vault://oc").
		SaveX(context.Background())
	r.Ent.ResourcePool.DeleteOneID(poolA.ID).ExecX(context.Background())

	if _, _, err := r.syncOnePool(context.Background(), poolA); err == nil {
		t.Fatal("expected a persist error for the deleted pool row")
	}

	// Pool B on the SAME endpoint. If the persist error had (wrongly) tripped the
	// breaker, this sync would fast-fail with a breaker-open error and never call
	// Inventory. Correct behavior: the breaker is still closed, so it runs.
	totalBefore := atomic.LoadInt32(&vc.total)
	poolB := r.Ent.ResourcePool.Create().
		SetName("pool-b").SetEndpoint(endpoint).SetSecretRef("vault://oc").
		SaveX(context.Background())
	if _, _, err := r.syncOnePool(context.Background(), poolB); err != nil {
		t.Fatalf("second sync should succeed (breaker must be closed), got %v", err)
	}
	if atomic.LoadInt32(&vc.total) != totalBefore+1 {
		t.Fatal("breaker wrongly opened by a DB persist error — vCenter not reached on the next sync")
	}
}

// #98 item 5b: two concurrent syncs of the SAME pool must be serialised by the
// per-pool mutex — their vCenter round-trips never overlap.
func TestSyncOnePool_SamePoolSerialised(t *testing.T) {
	vc := &countingVCenter{hold: 40 * time.Millisecond}
	r := poolSyncTestResolver(t, func() VCenterClient { return vc })

	pool := r.Ent.ResourcePool.Create().
		SetName("serial-pool").SetEndpoint("https://vc-serial").SetSecretRef("vault://oc").
		SaveX(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = r.syncOnePool(context.Background(), pool)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&vc.maxSeen); got != 1 {
		t.Fatalf("same-pool syncs overlapped (max in-flight=%d); per-pool mutex not enforced", got)
	}
	if got := atomic.LoadInt32(&vc.total); got != 4 {
		t.Fatalf("expected all 4 syncs to run, got %d", got)
	}
}

// #98 item 5b: syncs of DIFFERENT pools still run in parallel (the lock is
// per-pool, not global) — otherwise a slow vCenter would serialise the fleet.
func TestSyncOnePool_DifferentPoolsParallel(t *testing.T) {
	vc := &countingVCenter{hold: 60 * time.Millisecond}
	r := poolSyncTestResolver(t, func() VCenterClient { return vc })

	ids := make([]uuid.UUID, 3)
	for i := range ids {
		p := r.Ent.ResourcePool.Create().
			SetName("p" + string(rune('a'+i))).
			SetEndpoint("https://vc-" + string(rune('a'+i))).
			SetSecretRef("vault://oc").
			SaveX(context.Background())
		ids[i] = p.ID
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := r.Ent.ResourcePool.GetX(context.Background(), id)
			_, _, _ = r.syncOnePool(context.Background(), p)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&vc.maxSeen); got < 2 {
		t.Fatalf("different-pool syncs did not run in parallel (max in-flight=%d)", got)
	}
}
