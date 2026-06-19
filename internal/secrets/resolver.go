// Package secrets resolves a secret reference (stored on e.g. ResourcePool.secret_ref)
// to a credential, WITHOUT ever persisting the plaintext in the platform DB.
//
// Production backend (chosen 2026-06-19): Vaultwarden (C18). The reference is a
// pointer like "vault://<item-id>"; the backend resolves it at connect time and
// holds the credential only in memory. Dev/test use StaticResolver / EnvResolver.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

// StaticResolver resolves refs from an in-memory map (dev/test).
type StaticResolver struct {
	m map[string]Credential
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
	c, ok := s.m[ref]
	if !ok {
		return Credential{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return c, nil
}

// EnvResolver resolves refs of the form "env://USER_VAR,PASS_VAR" (or
// "env://USER_VAR,PASS_VAR,APIKEY_VAR") from the process environment. Useful for
// single-customer / air-gapped deployments before Vaultwarden is wired.
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
