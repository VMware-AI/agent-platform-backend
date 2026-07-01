package graph

import "context"

// syncSourceKey is the ctx key the three sync entry points use to tag
// their invocation source (fire-and-forget first sync, background ticker,
// or manual mutation). The tag flows through retrySync's inner func ctx
// so log lines from the attempt-level callback can prefix themselves with
// it without each caller re-passing an argument.
type syncSourceKey struct{}

// Sync sources — small constants for the ctx tag. Pick "manual" / "ticker"
// / "first-sync" so operators scanning logs can immediately tell whether
// the sync was operator-initiated, scheduled, or pool-create triggered.
const (
	syncSourceUnknown   = "unknown"
	syncSourceFirstSync = "first-sync"
	syncSourceTicker    = "ticker"
	syncSourceManual    = "manual"
)

// withSyncSource tags ctx so syncOnePool's log lines can attribute their
// work to a specific entry point. Use it at the boundary of each call site
// (CreateResourcePool's fire-and-forget goroutine, syncAllPools' loop,
// and the SyncResourcePool mutation).
func withSyncSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, syncSourceKey{}, source)
}

// syncSourceFromCtx returns the tag put on ctx by withSyncSource, or
// "unknown" when the caller didn't tag (typically tests that call
// syncOnePool directly).
func syncSourceFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(syncSourceKey{}).(string); ok && v != "" {
		return v
	}
	return syncSourceUnknown
}

// secretRefDisplay masks the secret_ref value for log output so an
// operator doesn't see the full secret reference in plain text (the
// reference IS encrypted at rest, but ops shouldn't have to think about
// that distinction when reading logs). Empty → "<none>"; otherwise the
// first 8 chars + "...". Anything beyond 8 chars is a UUID or vault path
// the operator can correlate with their secret store without leaking the
// full string.
func secretRefDisplay(ref string) string {
	if ref == "" {
		return "<none>"
	}
	if len(ref) <= 8 {
		return ref
	}
	return ref[:8] + "..."
}