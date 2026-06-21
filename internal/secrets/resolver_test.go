package secrets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticResolver(t *testing.T) {
	r := NewStaticResolver(map[string]Credential{
		"vault://oc1": {Username: "svc", Password: "p@ss"},
	})
	c, err := r.Resolve(context.Background(), "vault://oc1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Username != "svc" || c.Password != "p@ss" {
		t.Fatalf("unexpected cred: %+v", c)
	}
	if _, err := r.Resolve(context.Background(), "vault://missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnvResolver(t *testing.T) {
	t.Setenv("VC_USER", "administrator")
	t.Setenv("VC_PASS", "secret123")
	r := EnvResolver{}
	c, err := r.Resolve(context.Background(), "env://VC_USER,VC_PASS")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Username != "administrator" || c.Password != "secret123" {
		t.Fatalf("unexpected: %+v", c)
	}
	if _, err := r.Resolve(context.Background(), "vault://x"); err == nil {
		t.Fatal("non-env ref should error")
	}
}

func TestVaultwardenResolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/object/item/abc123" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"login":{"username":"svc-vc","password":"vpw"},"fields":[{"name":"api_key","value":"sk-1"}]}}`))
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	c, err := r.Resolve(context.Background(), "vault://abc123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Username != "svc-vc" || c.Password != "vpw" || c.APIKey != "sk-1" {
		t.Fatalf("unexpected cred: %+v", c)
	}
	if _, err := r.Resolve(context.Background(), "vault://nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing item want ErrNotFound, got %v", err)
	}
}

func TestStaticResolver_PutThenResolve(t *testing.T) {
	s := NewStaticResolver(nil)
	ref, err := s.Put(context.Background(), "agent-ui/vm-1", Credential{Username: "agent", Password: "p@ss"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref == "" {
		t.Fatal("Put returned empty ref")
	}
	got, err := s.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", ref, err)
	}
	if got.Username != "agent" || got.Password != "p@ss" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// distinct refs for distinct puts
	ref2, _ := s.Put(context.Background(), "agent-ui/vm-1", Credential{Password: "x"})
	if ref2 == ref {
		t.Fatal("Put should mint a distinct ref each call")
	}
}

// VaultwardenResolver and StaticResolver must both satisfy Resolver + Store.
var (
	_ Resolver = (*VaultwardenResolver)(nil)
	_ Store    = (*VaultwardenResolver)(nil)
	_ Resolver = (*StaticResolver)(nil)
	_ Store    = (*StaticResolver)(nil)
)
