package graph

import (
	"log"
	"sync"
	"time"

	"github.com/sony/gobreaker"
)

// poolBreakerRegistry maintains one *gobreaker.CircuitBreaker per vCenter
// endpoint, so a single bad vCenter can't take down the auto-sync ticker or
// stall every pool's first sync. All sync entry points (CreateResourcePool
// fire-and-forget, the background ticker, the manual syncResourcePool
// mutation) share this registry.
//
// Threshold / open-duration are constructor-time parameters sourced from
// POOL_SYNC_BREAKER_THRESHOLD and POOL_SYNC_BREAKER_BREAKER_OPEN_SECONDS;
// per-endpoint dynamic configuration is intentionally not supported.
type poolBreakerRegistry struct {
	mu       sync.Mutex
	breakers map[string]*gobreaker.CircuitBreaker
	settings gobreaker.Settings
}

// newPoolBreakerRegistry wires the registry with the same base settings for
// every endpoint. The Name field is filled in lazily per endpoint by get()
// so OnStateChange log lines can identify which vCenter tripped.
func newPoolBreakerRegistry(threshold uint32, openSec int) *poolBreakerRegistry {
	return &poolBreakerRegistry{
		breakers: make(map[string]*gobreaker.CircuitBreaker),
		settings: gobreaker.Settings{
			Name:        "?", // overwritten by get() per endpoint
			MaxRequests: 1,   // HalfOpen probe lets through exactly one request
			Interval:    0,   // no rolling window — counts are absolute since Open
			Timeout:     time.Duration(openSec) * time.Second,
			ReadyToTrip: func(c gobreaker.Counts) bool {
				return c.ConsecutiveFailures >= threshold
			},
			OnStateChange: func(name string, from, to gobreaker.State) {
				log.Printf("pool-sync breaker %s: %s → %s", name, from, to)
			},
		},
	}
}

// get returns (or creates) the breaker for an endpoint. Settings is a value
// type; we copy it so the registry's base settings aren't mutated when we
// stamp the endpoint into Name.
func (r *poolBreakerRegistry) get(endpoint string) *gobreaker.CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cb, ok := r.breakers[endpoint]; ok {
		return cb
	}
	s := r.settings
	s.Name = endpoint
	cb := gobreaker.NewCircuitBreaker(s)
	r.breakers[endpoint] = cb
	return cb
}