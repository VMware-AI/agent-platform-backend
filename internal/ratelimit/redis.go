package ratelimit

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// failIncr atomically increments the failure counter and, on the FIRST failure,
// arms a PEXPIRE so the window is fixed from that moment. Doing both in one Lua
// script avoids the lost-EXPIRE race (an INCR that leaves a key with no TTL would
// lock a user out permanently).
var failIncr = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// Redis is a fixed-window failure limiter backed by Redis, so the counter is
// GLOBAL across replicas (a load-balanced deployment can't be brute-forced N× by
// spreading attempts over N instances — the in-process Memory limiter can).
type Redis struct {
	rdb       *redis.Client
	threshold int
	window    time.Duration
}

// NewRedis returns a redis-backed limiter that blocks a key after threshold
// failures within window.
func NewRedis(rdb *redis.Client, threshold int, window time.Duration) *Redis {
	return &Redis{rdb: rdb, threshold: threshold, window: window}
}

func (r *Redis) key(key string) string { return "ratelimit:" + key }

// Blocked reports whether the key is at/over threshold. On a redis error it
// fails OPEN (returns false): during a redis outage we favor login availability
// over lockout; the brute-force exposure is bounded to the outage window and is
// logged. Flip to fail-closed only if the threat model demands it.
func (r *Redis) Blocked(ctx context.Context, key string) bool {
	n, err := r.rdb.Get(ctx, r.key(key)).Int()
	if err == redis.Nil {
		return false
	}
	if err != nil {
		log.Printf("ratelimit redis Blocked %s: %v (failing open)", key, err)
		return false
	}
	return n >= r.threshold
}

// Fail records a failed attempt (best effort; errors are logged, not surfaced).
func (r *Redis) Fail(ctx context.Context, key string) {
	if err := failIncr.Run(ctx, r.rdb, []string{r.key(key)}, r.window.Milliseconds()).Err(); err != nil {
		log.Printf("ratelimit redis Fail %s: %v", key, err)
	}
}

// Reset clears the counter for a key (call on success).
func (r *Redis) Reset(ctx context.Context, key string) {
	if err := r.rdb.Del(ctx, r.key(key)).Err(); err != nil {
		log.Printf("ratelimit redis Reset %s: %v", key, err)
	}
}
