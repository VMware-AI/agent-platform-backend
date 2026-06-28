package secrets

import (
	"context"
	"errors"
	"testing"
)

func TestEnvResolver_ThreePartIncludesAPIKey(t *testing.T) {
	t.Setenv("U", "user1")
	t.Setenv("P", "pass1")
	t.Setenv("K", "sk-key1")
	c, err := EnvResolver{}.Resolve(context.Background(), "env://U,P,K")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Username != "user1" || c.Password != "pass1" || c.APIKey != "sk-key1" {
		t.Fatalf("unexpected cred: %+v", c)
	}
}

func TestEnvResolver_TooFewParts(t *testing.T) {
	_, err := EnvResolver{}.Resolve(context.Background(), "env://ONLYONE")
	if err == nil {
		t.Fatal("single-part env spec must error")
	}
	// This is a malformed-spec error, distinct from a clean not-found.
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed spec should not be ErrNotFound: %v", err)
	}
}

func TestEnvResolver_EmptyEnvVarsIsNotFound(t *testing.T) {
	// Vars resolve to empty (unset) → treated as unresolved.
	_, err := EnvResolver{}.Resolve(context.Background(), "env://DEFINITELY_UNSET_USER,DEFINITELY_UNSET_PASS")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty env vars must map to ErrNotFound, got %v", err)
	}
}

// Put on a resolver whose backing map starts nil must lazily initialize it
// rather than panic (covers the s.m == nil branch).
func TestStaticResolver_PutInitializesNilMap(t *testing.T) {
	s := &StaticResolver{} // m is nil
	ref, err := s.Put(context.Background(), "k", Credential{APIKey: "v"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve after Put: %v", err)
	}
	if got.APIKey != "v" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// Delete on a resolver with a nil map is a safe no-op.
func TestStaticResolver_DeleteNilMapNoOp(t *testing.T) {
	s := &StaticResolver{}
	if err := s.Delete(context.Background(), "vault://anything"); err != nil {
		t.Fatalf("Delete on nil map should be a no-op, got %v", err)
	}
}
