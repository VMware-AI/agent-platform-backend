package graph

import (
	"testing"
	"time"
)

// TestPermCache_StaleReloadGuard reproduces the stale-reload race: a reader
// captures the generation, a revocation invalidates the user mid-read, and the
// in-flight reader then tries to cache its pre-revocation set. The generation
// guard must drop that put so the revoked permissions are NOT resurrected.
func TestPermCache_StaleReloadGuard(t *testing.T) {
	c := newPermCache(time.Minute)
	gen := c.generation()                        // reader captures gen before its DB read
	c.invalidate("u1")                           // a role revocation lands during the read
	c.put("u1", map[string]bool{"p": true}, gen) // in-flight reader tries to cache stale set
	if _, ok := c.get("u1"); ok {
		t.Fatal("stale permission set was cached despite an invalidation during the read")
	}
}

// TestPermCache_PutCachesWithoutInvalidation confirms the normal path still
// caches when no invalidation intervened.
func TestPermCache_PutCachesWithoutInvalidation(t *testing.T) {
	c := newPermCache(time.Minute)
	gen := c.generation()
	c.put("u1", map[string]bool{"p": true}, gen)
	if set, ok := c.get("u1"); !ok || !set["p"] {
		t.Fatalf("expected cached set, got ok=%v set=%v", ok, set)
	}
}

// TestPermCache_NilSafe confirms a nil cache (caching disabled) is a no-op.
func TestPermCache_NilSafe(t *testing.T) {
	var c *permCache
	if c.generation() != 0 {
		t.Fatal("nil generation should be 0")
	}
	c.put("u", map[string]bool{"p": true}, 0) // no panic
	if _, ok := c.get("u"); ok {
		t.Fatal("nil cache should never report a hit")
	}
	c.invalidate("u")
	c.clear()
}
