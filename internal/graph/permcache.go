package graph

import (
	"sync"
	"time"
)

// permCache memoizes a user's effective custom-role permission set so the
// @hasPermission directive doesn't hit the DB on every guarded field. Entries
// expire after a short TTL; mutations that change roles invalidate eagerly.
// nil-safe: a nil *permCache simply disables caching (always query).
//
// ⚠️ PROCESS-LOCAL: invalidate/clear only evict THIS replica's map. Under a
// multi-replica deployment a role/permission revocation does NOT propagate to
// other replicas — they keep honoring the stale permission set until its TTL
// expires. It is therefore disabled by default (PERM_CACHE_TTL_SECONDS=0) and
// should be enabled single-replica only, until a shared (Redis pub/sub)
// invalidation channel lands.
type permCache struct {
	mu  sync.Mutex
	m   map[string]permCacheEntry
	ttl time.Duration
	// gen advances on every invalidate/clear. A reader captures gen BEFORE its DB
	// read and passes it to put(); if gen moved (a revocation landed during the
	// read) put drops the now-stale set instead of caching it — closing the
	// stale-reload race where a put could resurrect a just-revoked permission set
	// for the full TTL.
	gen uint64
}

type permCacheEntry struct {
	set map[string]bool
	exp time.Time
}

func newPermCache(ttl time.Duration) *permCache {
	return &permCache{m: make(map[string]permCacheEntry), ttl: ttl}
}

func (c *permCache) get(userID string) (map[string]bool, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[userID]
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.set, true
}

// generation returns the current invalidation generation. Capture it before a DB
// read and feed it back to put() so a revocation that lands during the read is
// not overwritten by a stale put.
func (c *permCache) generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *permCache) put(userID string, set map[string]bool, genSeen uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != genSeen {
		return // invalidated during the caller's DB read — don't cache a stale set
	}
	c.m[userID] = permCacheEntry{set: set, exp: time.Now().Add(c.ttl)}
}

func (c *permCache) invalidate(userID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, userID)
	c.gen++
}

func (c *permCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = make(map[string]permCacheEntry)
	c.gen++
}
