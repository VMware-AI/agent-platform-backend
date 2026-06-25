package graph

import (
	"context"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
)

// loaders.go implements a request-scoped, prime-able cache (the dataloader
// pattern, eager variant) for the User and VirtualKey rows that the Agent.owner
// / Agent.apiKey / Agent.credentials field resolvers need.
//
// Why eager priming instead of a time-windowed batcher: gqlgen (v0.17.x)
// resolves a list's elements sequentially — FieldSet.Dispatch runs one object's
// concurrent fields and wg.Wait()s before the parent advances to the next
// element. So a window-based loader could only ever coalesce one row's handful
// of fields, never the whole page. Instead, the Agents/Agent resolver — which
// already holds every ent.Agent row — primes the cache with ONE batched
// `WHERE id IN (...)` query per entity type up front; the field resolvers then
// read from the cache (no DB round-trip). This makes the page O(1) in User /
// VirtualKey queries regardless of row count.
//
// No third-party dataloader dependency is used (license-vetting overhead) — this
// is a small, well-scoped, hand-rolled cache.
//
// Lifecycle: one Loaders instance per HTTP response, injected via context by
// InstallLoaders. A field resolver invoked without an installed cache (e.g. a
// unit test calling the resolver directly) falls back to a single-row Get, so
// resolvers never hard-depend on the middleware.

// Loaders is a per-request cache of related rows, primed by list resolvers and
// read by field resolvers. Safe for the concurrent field resolution gqlgen runs
// within a single object.
type Loaders struct {
	client *ent.Client

	mu          sync.Mutex
	users       map[uuid.UUID]*ent.User       // nil value = looked up, absent (deleted)
	virtualKeys map[uuid.UUID]*ent.VirtualKey // nil value = looked up, absent (deleted)
}

type loadersCtxKey struct{}

// NewLoaders builds a fresh per-request cache backed by the given client.
func NewLoaders(client *ent.Client) *Loaders {
	return &Loaders{
		client:      client,
		users:       make(map[uuid.UUID]*ent.User),
		virtualKeys: make(map[uuid.UUID]*ent.VirtualKey),
	}
}

// WithLoaders stores a per-request cache on the context.
func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loadersCtxKey{}, l)
}

// loadersFrom returns the request's cache, or nil if none was installed.
func loadersFrom(ctx context.Context) *Loaders {
	l, _ := ctx.Value(loadersCtxKey{}).(*Loaders)
	return l
}

// PrimeUsers batch-loads the given user ids into the cache in a single query,
// skipping ids already cached. Absent ids are recorded as nil (deleted owner →
// the field resolver returns nil, not an error). Best-effort: a query error is
// returned so the caller can decide, but priming failure never corrupts state.
func (l *Loaders) PrimeUsers(ctx context.Context, ids []uuid.UUID) error {
	missing := l.uncachedUsers(ids)
	if len(missing) == 0 {
		return nil
	}
	rows, err := l.client.User.Query().Where(user.IDIn(missing...)).All(ctx)
	if err != nil {
		return err
	}
	found := make(map[uuid.UUID]*ent.User, len(rows))
	for _, row := range rows {
		found[row.ID] = row
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range missing {
		l.users[id] = found[id] // present → row; absent → nil (records the miss)
	}
	return nil
}

// PrimeVirtualKeys batch-loads the given virtual-key ids into the cache in a
// single query, skipping ids already cached. Absent ids are recorded as nil.
func (l *Loaders) PrimeVirtualKeys(ctx context.Context, ids []uuid.UUID) error {
	missing := l.uncachedVirtualKeys(ids)
	if len(missing) == 0 {
		return nil
	}
	rows, err := l.client.VirtualKey.Query().Where(virtualkey.IDIn(missing...)).All(ctx)
	if err != nil {
		return err
	}
	found := make(map[uuid.UUID]*ent.VirtualKey, len(rows))
	for _, row := range rows {
		found[row.ID] = row
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range missing {
		l.virtualKeys[id] = found[id]
	}
	return nil
}

// uncachedUsers returns the distinct ids not yet present in the user cache.
func (l *Loaders) uncachedUsers(ids []uuid.UUID) []uuid.UUID {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, cached := l.users[id]; !cached {
			out = append(out, id)
		}
	}
	return out
}

// uncachedVirtualKeys returns the distinct ids not yet present in the vk cache.
func (l *Loaders) uncachedVirtualKeys(ids []uuid.UUID) []uuid.UUID {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, cached := l.virtualKeys[id]; !cached {
			out = append(out, id)
		}
	}
	return out
}

// userFromCache returns (row, true) when id was primed; row is nil for a primed
// miss (deleted user). (nil, false) means not primed — caller must fetch.
func (l *Loaders) userFromCache(id uuid.UUID) (*ent.User, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	row, ok := l.users[id]
	return row, ok
}

// virtualKeyFromCache mirrors userFromCache for virtual keys.
func (l *Loaders) virtualKeyFromCache(id uuid.UUID) (*ent.VirtualKey, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	row, ok := l.virtualKeys[id]
	return row, ok
}

// loadUser returns the User for id, preferring the primed request cache and
// falling back to a single-row fetch on a cache miss (or no cache installed). A
// missing row yields (nil, nil) so owner/credentials nil-safety is preserved.
func (r *Resolver) loadUser(ctx context.Context, id uuid.UUID) (*ent.User, error) {
	if l := loadersFrom(ctx); l != nil {
		if row, ok := l.userFromCache(id); ok {
			return row, nil
		}
	}
	u, err := r.Ent.User.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// loadVirtualKey returns the VirtualKey for id, preferring the primed cache and
// falling back to a single-row fetch. Missing row → (nil, nil).
func (r *Resolver) loadVirtualKey(ctx context.Context, id uuid.UUID) (*ent.VirtualKey, error) {
	if l := loadersFrom(ctx); l != nil {
		if row, ok := l.virtualKeyFromCache(id); ok {
			return row, nil
		}
	}
	vk, err := r.Ent.VirtualKey.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return vk, nil
}

// primeAgentRelations pre-batches the owners and virtual keys referenced by a
// page of agents into the request cache (one query each), so the per-row
// owner/apiKey/credentials field resolvers hit the cache instead of the DB.
// No-op when no request cache is installed (e.g. direct unit-test calls).
func (r *Resolver) primeAgentRelations(ctx context.Context, agents []*ent.Agent) {
	l := loadersFrom(ctx)
	if l == nil || len(agents) == 0 {
		return
	}
	ownerIDs := make([]uuid.UUID, 0, len(agents))
	keyIDs := make([]uuid.UUID, 0, len(agents))
	for _, a := range agents {
		ownerIDs = append(ownerIDs, a.OwnerUserID)
		if a.VirtualKeyID != nil {
			keyIDs = append(keyIDs, *a.VirtualKeyID)
		}
	}
	// Priming is best-effort: an error here just means the field resolvers fall
	// back to per-row fetches (correctness preserved), so we ignore it. The
	// field resolvers surface any real query error themselves.
	_ = l.PrimeUsers(ctx, ownerIDs)
	_ = l.PrimeVirtualKeys(ctx, keyIDs)
}

// loaderInstaller is the subset of the gqlgen handler used to attach the
// per-response loader middleware. Both *handler.Server and the test harness
// satisfy it.
type loaderInstaller interface {
	AroundResponses(graphql.ResponseMiddleware)
}

// InstallLoaders wires a fresh per-response Loaders cache into the context
// before each GraphQL response's fields are resolved, so the Agents list
// resolver can prime it and the Agent field resolvers can read it. Call once at
// server setup.
func (r *Resolver) InstallLoaders(srv loaderInstaller) {
	srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		ctx = WithLoaders(ctx, NewLoaders(r.Ent))
		return next(ctx)
	})
}
