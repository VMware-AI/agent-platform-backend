// Package ratelimit throttles repeated failures (e.g. login brute-force) by key.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter throttles repeated failures per key. Implementations are safe for
// concurrent use.
type Limiter interface {
	// Blocked reports whether the key has reached the failure threshold within
	// the current window.
	Blocked(ctx context.Context, key string) bool
	// Fail records a failed attempt.
	Fail(ctx context.Context, key string)
	// Reset clears the counter for a key (call on success).
	Reset(ctx context.Context, key string)
}

type entry struct {
	count   int
	resetAt time.Time
}

// Memory is an in-process fixed-window failure limiter. It is correct for a
// single instance; multi-replica deployments need a shared store (redis) so the
// counter is global — wire a redis Limiter there instead.
type Memory struct {
	mu        sync.Mutex
	entries   map[string]*entry
	threshold int
	window    time.Duration
	now       func() time.Time // injectable for tests
}

// NewMemory returns a limiter that blocks a key after threshold failures within
// window; the window resets once it elapses or on Reset.
func NewMemory(threshold int, window time.Duration) *Memory {
	return &Memory{
		entries:   make(map[string]*entry),
		threshold: threshold,
		window:    window,
		now:       time.Now,
	}
}

func (m *Memory) Blocked(_ context.Context, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	if e == nil || m.now().After(e.resetAt) {
		return false
	}
	return e.count >= m.threshold
}

func (m *Memory) Fail(_ context.Context, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if len(m.entries) > 10000 {
		m.sweep(now) // bound growth under a distributed attack
	}
	e := m.entries[key]
	if e == nil || now.After(e.resetAt) {
		e = &entry{resetAt: now.Add(m.window)}
		m.entries[key] = e
	}
	e.count++
}

func (m *Memory) Reset(_ context.Context, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
}

// sweep drops expired entries; caller holds the lock.
func (m *Memory) sweep(now time.Time) {
	for k, e := range m.entries {
		if now.After(e.resetAt) {
			delete(m.entries, k)
		}
	}
}
