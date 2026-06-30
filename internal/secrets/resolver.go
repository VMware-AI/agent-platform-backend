// Package secrets resolves a secret reference (stored on e.g. ResourcePool.secret_ref)
// to a credential.
//
// The single backend, used in EVERY environment (no dev/prod split), is DBStore:
// credentials live in the platform_secrets table, ENCRYPTED at rest (AES-256-GCM)
// under one symmetric key (SECRETS_ENCRYPTION_KEY). They survive backend restarts
// and there is one code path to reason about. StaticResolver is an in-memory double
// for tests only; EnvResolver reads credentials from the process environment for
// niche air-gapped setups. The plaintext credential is held only in memory after a
// Resolve; only the opaque ref is stored on owning rows.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ErrNotFound is returned when a reference cannot be resolved.
var ErrNotFound = errors.New("secret reference not found")

// Credential is a resolved secret. Held only in memory; never logged/persisted.
type Credential struct {
	Username string
	Password string
	APIKey   string
}

// Resolver turns a secret_ref into a Credential.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (Credential, error)
}

// Store persists a credential and returns a secret_ref pointer (e.g.
// "vault://<item-id>"). Used for credentials minted at runtime — e.g. a
// rotated agent UI password (LLD-08 §6) — so the platform DB only ever holds
// the pointer, never the plaintext.
type Store interface {
	Put(ctx context.Context, name string, cred Credential) (ref string, err error)
	// Delete removes a previously Put secret by its ref, so deleting/rotating the
	// owning row (e.g. a gateway master key) doesn't orphan it in the store.
	// Idempotent: a missing ref is not an error (desired end-state already holds).
	Delete(ctx context.Context, ref string) error
}

// StaticResolver resolves refs from an in-memory map. It is a TEST-ONLY double
// (the production backend is the encrypted DBStore); it implements Store so tests
// can exercise the rotation write path without a database. A single instance may
// be shared across goroutines in a test, so mu guards m/seq: an unguarded
// concurrent map read+write would raise Go's unrecoverable "concurrent map" fatal
// error.
type StaticResolver struct {
	mu  sync.RWMutex
	m   map[string]Credential
	seq int
}

// NewStaticResolver returns a resolver backed by the given map (copied).
func NewStaticResolver(m map[string]Credential) *StaticResolver {
	cp := make(map[string]Credential, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &StaticResolver{m: cp}
}

func (s *StaticResolver) Resolve(_ context.Context, ref string) (Credential, error) {
	s.mu.RLock()
	c, ok := s.m[ref]
	s.mu.RUnlock()
	if !ok {
		return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return c, nil
}

// Put stores the credential under a generated ref (dev/test in-memory Store).
func (s *StaticResolver) Put(_ context.Context, name string, cred Credential) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]Credential{}
	}
	s.seq++
	ref := fmt.Sprintf("vault://%s-%d", name, s.seq)
	s.m[ref] = cred
	return ref, nil
}

// Delete removes a ref from the in-memory map (idempotent — deleting an absent
// ref is a no-op).
func (s *StaticResolver) Delete(_ context.Context, ref string) error {
	s.mu.Lock()
	delete(s.m, ref)
	s.mu.Unlock()
	return nil
}

// EnvResolver resolves refs of the form "env://USER_VAR,PASS_VAR" (or
// "env://USER_VAR,PASS_VAR,APIKEY_VAR") from the process environment. A niche
// escape hatch for air-gapped setups that inject creds via env (resolved at read
// time, never stored); the primary store is the encrypted DBStore.
type EnvResolver struct{}

func (EnvResolver) Resolve(_ context.Context, ref string) (Credential, error) {
	spec, ok := strings.CutPrefix(ref, "env://")
	if !ok {
		return Credential{}, fmt.Errorf("%w: EnvResolver expects env:// ref, got %q", ErrNotFound, ref)
	}
	parts := strings.Split(spec, ",")
	if len(parts) < 2 {
		return Credential{}, fmt.Errorf("env:// ref needs USER_VAR,PASS_VAR[,APIKEY_VAR], got %q", ref)
	}
	c := Credential{
		Username: os.Getenv(strings.TrimSpace(parts[0])),
		Password: os.Getenv(strings.TrimSpace(parts[1])),
	}
	if len(parts) >= 3 {
		c.APIKey = os.Getenv(strings.TrimSpace(parts[2]))
	}
	if c.Username == "" || c.Password == "" {
		return Credential{}, fmt.Errorf("%w: env vars empty for %q", ErrNotFound, ref)
	}
	return c, nil
}
