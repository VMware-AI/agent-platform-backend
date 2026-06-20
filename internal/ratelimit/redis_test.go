package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisLimiter(t *testing.T, threshold int, window time.Duration) (*Redis, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })
	return NewRedis(rdb, threshold, window), mr, rdb
}

func TestRedis_BlocksAfterThreshold(t *testing.T) {
	l, _, _ := newRedisLimiter(t, 3, time.Hour)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		l.Fail(ctx, "1.2.3.4")
		if l.Blocked(ctx, "1.2.3.4") {
			t.Fatalf("blocked too early after %d failures", i+1)
		}
	}
	l.Fail(ctx, "1.2.3.4") // 3rd → at threshold
	if !l.Blocked(ctx, "1.2.3.4") {
		t.Fatal("should be blocked at threshold")
	}
	// A different key is unaffected.
	if l.Blocked(ctx, "9.9.9.9") {
		t.Fatal("unrelated key must not be blocked")
	}
}

func TestRedis_WindowExpires(t *testing.T) {
	l, mr, _ := newRedisLimiter(t, 1, 15*time.Minute)
	ctx := context.Background()
	l.Fail(ctx, "k")
	if !l.Blocked(ctx, "k") {
		t.Fatal("should be blocked")
	}
	mr.FastForward(16 * time.Minute) // window elapses → counter key expires
	if l.Blocked(ctx, "k") {
		t.Fatal("block should lift after the window")
	}
}

func TestRedis_ResetClears(t *testing.T) {
	l, _, _ := newRedisLimiter(t, 2, time.Hour)
	ctx := context.Background()
	l.Fail(ctx, "k")
	l.Fail(ctx, "k")
	if !l.Blocked(ctx, "k") {
		t.Fatal("should be blocked")
	}
	l.Reset(ctx, "k") // success clears the counter
	if l.Blocked(ctx, "k") {
		t.Fatal("reset must clear the block")
	}
}

// The whole point of a redis limiter: two replicas sharing one redis see a single
// global counter, so brute-force protection isn't defeated by load balancing.
func TestRedis_SharedAcrossReplicas(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	mk := func() *Redis {
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		return NewRedis(rdb, 3, time.Hour)
	}
	replicaA, replicaB := mk(), mk()
	ctx := context.Background()

	replicaA.Fail(ctx, "ip")
	replicaB.Fail(ctx, "ip")
	replicaA.Fail(ctx, "ip") // 3 failures spread across two replicas

	if !replicaB.Blocked(ctx, "ip") {
		t.Fatal("replica B must see failures recorded on replica A (shared counter)")
	}
}
