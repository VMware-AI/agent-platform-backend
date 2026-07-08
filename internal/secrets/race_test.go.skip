package secrets

import (
	"context"
	"sync"
	"testing"
)

// TestStaticResolver_ConcurrentAccess exercises the shared StaticResolver under
// concurrent Resolve/Put/Delete — the dev/air-gap path where a single instance is
// shared by request goroutines (credential-intake mutations + secret resolves)
// and the agent-manager heartbeat goroutine (rotation Put). Before the mu guard
// this raised Go's unrecoverable "concurrent map read and map write" fatal error,
// crashing the whole control plane. Must pass under `go test -race`.
func TestStaticResolver_ConcurrentAccess(t *testing.T) {
	s := NewStaticResolver(map[string]Credential{"vault://seed": {APIKey: "x"}})
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _, _ = s.Put(ctx, "k", Credential{APIKey: "v"}) }()
		go func() { defer wg.Done(); _, _ = s.Resolve(ctx, "vault://seed") }()
		go func() { defer wg.Done(); _ = s.Delete(ctx, "vault://seed") }()
	}
	wg.Wait()
}
