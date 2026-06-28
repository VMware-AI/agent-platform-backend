package graph

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// bug审计0628b #3 (#64): a secret Put into the store before the owning DB row is
// saved must NOT be orphaned when that Save fails. Every create/update path that
// mints a NEW ref (resolve*SecretRef minted==true) has to retire it on the
// Save-error path; a caller-supplied existing ref must be left untouched.
//
// trackingStore wraps the in-memory StaticResolver and keeps a live-ref set so a
// test can assert there is no orphan after a failed mutation (live == referenced)
// and that a delete actually fired. It satisfies both secrets.Resolver and
// secrets.Store, exactly like the StaticResolver the resolvers use in dev/CI.
type trackingStore struct {
	inner   *secrets.StaticResolver
	mu      sync.Mutex
	live    map[string]struct{}
	puts    int
	deletes int
}

func newTrackingStore() *trackingStore {
	return &trackingStore{inner: secrets.NewStaticResolver(nil), live: map[string]struct{}{}}
}

func (s *trackingStore) Resolve(ctx context.Context, ref string) (secrets.Credential, error) {
	return s.inner.Resolve(ctx, ref)
}

func (s *trackingStore) Put(ctx context.Context, name string, cred secrets.Credential) (string, error) {
	ref, err := s.inner.Put(ctx, name, cred)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.live[ref] = struct{}{}
	s.puts++
	s.mu.Unlock()
	return ref, nil
}

func (s *trackingStore) Delete(ctx context.Context, ref string) error {
	if err := s.inner.Delete(ctx, ref); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.live, ref)
	s.deletes++
	s.mu.Unlock()
	return nil
}

func (s *trackingStore) liveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live)
}

// A duplicate-name CreateModelGateway Puts the masterKey, then trips the unique
// name constraint on Save. The just-minted secret must be retired (no orphan),
// while the first gateway's secret stays live.
func TestCreateModelGateway_DBFailDoesNotOrphanSecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	store := newTrackingStore()
	r.Secrets = store
	ctx := adminCtx()
	mr := &mutationResolver{r}

	k1 := "sk-first"
	g1, err := mr.CreateModelGateway(ctx, litellmGatewayInput("dup-gw", "http://lite:4000", &k1))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	ref1 := r.Ent.GatewayConnection.GetX(ctx, uuid.MustParse(g1.ID)).MasterKeyRef
	if store.liveCount() != 1 {
		t.Fatalf("after first create want 1 live secret, got %d", store.liveCount())
	}

	// Same name → unique-constraint failure on Save, AFTER the second masterKey
	// was Put to the store.
	k2 := "sk-second-leaked"
	if _, err := mr.CreateModelGateway(ctx, litellmGatewayInput("dup-gw", "http://lite:4000", &k2)); err == nil {
		t.Fatal("duplicate-name create must fail on the unique constraint")
	}

	if store.puts != 2 {
		t.Fatalf("expected the second masterKey to be Put (puts=2), got %d", store.puts)
	}
	if store.deletes != 1 {
		t.Fatalf("expected the orphaned ref to be Deleted (deletes=1), got %d -- orphan leaked", store.deletes)
	}
	if store.liveCount() != 1 {
		t.Fatalf("orphan secret leaked: want 1 live secret (the first gateway's), got %d", store.liveCount())
	}
	// The first gateway's secret is intact; nothing else references a live ref.
	if _, err := store.Resolve(ctx, ref1); err != nil {
		t.Fatalf("first gateway's secret must survive: %v", err)
	}
}

// RegisterGatewayConnection Puts the masterKey OUTSIDE the txn, so a failed insert
// (here: duplicate name) + rollback must still retire the minted ref.
func TestRegisterGatewayConnection_DBFailDoesNotOrphanSecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	store := newTrackingStore()
	r.Secrets = store
	injectFakeGatewayModels(r) // first register becomes default; fake its model sync
	ctx := adminCtx()
	mr := &mutationResolver{r}

	mk1 := "sk-master-1"
	if _, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "rgw", Endpoint: "https://lite", MasterKey: &mk1,
	}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if store.liveCount() != 1 {
		t.Fatalf("after first register want 1 live secret, got %d", store.liveCount())
	}

	mk2 := "sk-master-2-leaked"
	if _, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "rgw", Endpoint: "https://lite", MasterKey: &mk2,
	}); err == nil {
		t.Fatal("duplicate-name register must fail on the unique constraint")
	}
	if store.deletes != 1 {
		t.Fatalf("expected the out-of-txn minted ref to be Deleted (deletes=1), got %d -- orphan leaked", store.deletes)
	}
	if store.liveCount() != 1 {
		t.Fatalf("orphan secret leaked across the rolled-back txn: want 1 live, got %d", store.liveCount())
	}
}

// CreateResourcePool Puts the vCenter username/password BEFORE the row is saved.
// resourcepool.name is not unique, so we trip a different but equally real Save
// failure: endpoint is .NotEmpty(), so an empty endpoint fails ent validation on
// Save after the credential was already Put. The freshly-minted credential must
// be retired (no orphaned plaintext password).
func TestCreateResourcePool_DBFailDoesNotOrphanSecret(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	store := newTrackingStore()
	r.Secrets = store
	ctx := adminCtx()
	mr := &mutationResolver{r}

	u, p := "admin@vsphere.local", "pw-leaked"
	if _, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "pool", Endpoint: "", Username: &u, Password: &p, // empty endpoint -> Save validation fails
	}); err == nil {
		t.Fatal("create with empty endpoint must fail ent validation on Save")
	}
	if store.puts != 1 {
		t.Fatalf("expected the vCenter credential to be Put (puts=1), got %d", store.puts)
	}
	if store.deletes != 1 {
		t.Fatalf("expected the orphaned vCenter credential to be Deleted (deletes=1), got %d -- orphan leaked", store.deletes)
	}
	if store.liveCount() != 0 {
		t.Fatalf("orphan secret leaked: want 0 live secrets after the failed create, got %d", store.liveCount())
	}
}

// Regression guard for the minted/supplied distinction: when the caller passes a
// PRE-EXISTING secretRef (not a raw credential), a subsequent Save failure must
// NOT delete that ref — it is the caller's, possibly shared by another row. Only
// freshly-minted refs are retired on failure.
func TestCreateResourcePool_DBFailKeepsCallerSuppliedRef(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	existing := "vault://shared-cred"
	// Pre-seed the shared credential as if another row already owns it: the inner
	// store resolves the ref, and we mark it live so an erroneous Delete is visible.
	store := &trackingStore{
		inner: secrets.NewStaticResolver(map[string]secrets.Credential{existing: {Username: "x", Password: "y"}}),
		live:  map[string]struct{}{existing: {}},
	}
	r.Secrets = store
	ctx := adminCtx()
	mr := &mutationResolver{r}

	deletesBefore := store.deletes
	if _, err := mr.CreateResourcePool(ctx, model.CreateResourcePoolInput{
		Name: "pool", Endpoint: "", SecretRef: &existing, // empty endpoint -> Save fails
	}); err == nil {
		t.Fatal("create with empty endpoint must fail")
	}
	if store.deletes != deletesBefore {
		t.Fatalf("a caller-supplied existing ref must NOT be deleted on Save failure (deletes went %d->%d)", deletesBefore, store.deletes)
	}
	if store.puts != 0 {
		t.Fatalf("no raw credential was submitted, so nothing should be Put (puts=%d)", store.puts)
	}
	if _, err := store.Resolve(ctx, existing); err != nil {
		t.Fatalf("caller-supplied ref must still resolve after the failed create: %v", err)
	}
}
