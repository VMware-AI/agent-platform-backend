package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/platformsecret"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

const testKey = "unit-test-encryption-key-32bytes-or-more"

func newDBStore(t *testing.T, key string) *DBStore {
	t.Helper()
	client, err := store.Open(context.Background(), "", true) // in-memory sqlite
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	s, err := NewDBStore(client, key)
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	return s
}

// Put then Resolve returns the original credential, and the stored row holds
// CIPHERTEXT — never the plaintext password/api_key.
func TestDBStore_EncryptsAtRestAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newDBStore(t, testKey)

	cred := Credential{Username: "administrator@vsphere.local", Password: "p@ssw0rd!", APIKey: "sk-secret-123"}
	ref, err := s.Put(ctx, "vcenter/pool-1", cred)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Stored row must NOT contain the plaintext secrets.
	row := s.ent.PlatformSecret.Query().Where(platformsecret.Ref(ref)).OnlyX(ctx)
	if row.Password == cred.Password || row.Password == "" {
		t.Errorf("password stored in clear (or empty): %q", row.Password)
	}
	if row.APIKey == cred.APIKey || row.APIKey == "" {
		t.Errorf("api_key stored in clear (or empty): %q", row.APIKey)
	}
	if row.Username != cred.Username {
		t.Errorf("username should be stored in clear, got %q", row.Username)
	}

	got, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != cred {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, cred)
	}
}

// The exact bug this replaces: a fresh DBStore instance (same DB + same key —
// i.e. a backend restart) still resolves a previously stored secret. The store
// holds NO secret state; everything is in PG, and the key re-derives identically.
func TestDBStore_SurvivesRestart(t *testing.T) {
	ctx := context.Background()
	client, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer client.Close()

	before, err := NewDBStore(client, testKey)
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	ref, err := before.Put(ctx, "gw/master", Credential{APIKey: "sk-master"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Simulate a process restart: a brand-new DBStore over the same DB + key.
	after, err := NewDBStore(client, testKey)
	if err != nil {
		t.Fatalf("NewDBStore (restart): %v", err)
	}
	got, err := after.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve after restart: %v", err)
	}
	if got.APIKey != "sk-master" {
		t.Errorf("secret lost across restart: got %q", got.APIKey)
	}
}

// A different key cannot decrypt — GCM auth fails loudly rather than returning
// silent garbage.
func TestDBStore_WrongKeyFailsToDecrypt(t *testing.T) {
	ctx := context.Background()
	client, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer client.Close()

	good, _ := NewDBStore(client, "key-one")
	ref, err := good.Put(ctx, "x", Credential{Password: "topsecret"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	bad, _ := NewDBStore(client, "key-two")
	if _, err := bad.Resolve(ctx, ref); err == nil {
		t.Fatal("decrypt with the wrong key must fail, not return garbage")
	}
}

// An empty key is a fail-fast construction error (never silently store plaintext).
func TestNewDBStore_RequiresKey(t *testing.T) {
	client, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer client.Close()
	if _, err := NewDBStore(client, ""); err == nil {
		t.Fatal("NewDBStore with empty key must error")
	}
}

// Empty secret fields stay empty (unset stays unset), and resolving a missing ref
// is ErrNotFound; Delete is idempotent.
func TestDBStore_EmptyFieldsAndLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newDBStore(t, testKey)

	ref, err := s.Put(ctx, "user-only", Credential{Username: "svc"}) // no password / api_key
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Username != "svc" || got.Password != "" || got.APIKey != "" {
		t.Errorf("empty fields should round-trip empty: %+v", got)
	}

	if _, err := s.Resolve(ctx, "vault://missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing ref want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, ref); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := s.Delete(ctx, ref); err != nil {
		t.Errorf("Delete must be idempotent, got %v", err)
	}
	if _, err := s.Resolve(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete want ErrNotFound, got %v", err)
	}
}
