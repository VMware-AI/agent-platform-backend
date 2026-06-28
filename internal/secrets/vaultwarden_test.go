package secrets

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Resolve error branches (the happy path is covered in resolver_test.go) ---

func TestVaultwardenResolver_Resolve_RejectsNonVaultRef(t *testing.T) {
	r := NewVaultwardenResolver("http://127.0.0.1:0")
	_, err := r.Resolve(context.Background(), "env://A,B")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for non-vault ref, got %v", err)
	}
}

func TestVaultwardenResolver_Resolve_Non2xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	_, err := r.Resolve(context.Background(), "vault://x")
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("want status-500 error, got %v", err)
	}
	// A generic 5xx is NOT a not-found.
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("5xx must not be reported as ErrNotFound: %v", err)
	}
}

func TestVaultwardenResolver_Resolve_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{not json")
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	_, err := r.Resolve(context.Background(), "vault://x")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestVaultwardenResolver_Resolve_SuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"success":false}`)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	_, err := r.Resolve(context.Background(), "vault://x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("bw success=false must map to ErrNotFound, got %v", err)
	}
}

func TestVaultwardenResolver_Resolve_TransportError(t *testing.T) {
	// Point at a server that is immediately closed → Do() returns a connection
	// error, exercising the v.http.Do error branch.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	r := NewVaultwardenResolver(url)
	_, err := r.Resolve(context.Background(), "vault://x")
	if err == nil || !strings.Contains(err.Error(), "vaultwarden") {
		t.Fatalf("want vaultwarden transport error, got %v", err)
	}
}

// --- Put ---

func TestVaultwardenResolver_Put_Success(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/object/item" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "bad content-type", http.StatusUnsupportedMediaType)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":"new-id-1"}}`)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	ref, err := r.Put(context.Background(), "agent-ui/vm-9", Credential{Username: "agent", Password: "p@ss"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref != "vault://new-id-1" {
		t.Fatalf("ref = %q, want vault://new-id-1", ref)
	}
	if !strings.Contains(gotBody, `"agent-ui/vm-9"`) || !strings.Contains(gotBody, `"p@ss"`) {
		t.Fatalf("request body missing name/credential: %s", gotBody)
	}
}

func TestVaultwardenResolver_Put_Non2xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	_, err := r.Put(context.Background(), "n", Credential{})
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("want status-401 error, got %v", err)
	}
}

func TestVaultwardenResolver_Put_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{broken")
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	_, err := r.Put(context.Background(), "n", Credential{})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestVaultwardenResolver_Put_SuccessFalseOrEmptyID(t *testing.T) {
	cases := map[string]string{
		"success=false": `{"success":false,"data":{"id":"x"}}`,
		"empty id":      `{"success":true,"data":{"id":""}}`,
		"missing data":  `{"success":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()

			r := NewVaultwardenResolver(srv.URL)
			_, err := r.Put(context.Background(), "n", Credential{})
			if err == nil || !strings.Contains(err.Error(), "failure or empty id") {
				t.Fatalf("want failure/empty-id error, got %v", err)
			}
		})
	}
}

func TestVaultwardenResolver_Put_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	r := NewVaultwardenResolver(url)
	_, err := r.Put(context.Background(), "n", Credential{})
	if err == nil || !strings.Contains(err.Error(), "put") {
		t.Fatalf("want vaultwarden put transport error, got %v", err)
	}
}

// --- Delete ---

func TestVaultwardenResolver_Delete_Success(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	if err := r.Delete(context.Background(), "vault://item-7"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/object/item/item-7" {
		t.Fatalf("got %s %s, want DELETE /object/item/item-7", gotMethod, gotPath)
	}
}

func TestVaultwardenResolver_Delete_404IsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	if err := r.Delete(context.Background(), "vault://already-gone"); err != nil {
		t.Fatalf("404 must read as success (idempotent), got %v", err)
	}
}

func TestVaultwardenResolver_Delete_Non2xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	err := r.Delete(context.Background(), "vault://x")
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("want status-500 error, got %v", err)
	}
}

func TestVaultwardenResolver_Delete_RejectsNonVaultRef(t *testing.T) {
	r := NewVaultwardenResolver("http://127.0.0.1:0")
	err := r.Delete(context.Background(), "env://A,B")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for non-vault ref, got %v", err)
	}
}

func TestVaultwardenResolver_Delete_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	r := NewVaultwardenResolver(url)
	err := r.Delete(context.Background(), "vault://x")
	if err == nil || !strings.Contains(err.Error(), "delete") {
		t.Fatalf("want vaultwarden delete transport error, got %v", err)
	}
}

// NewVaultwardenResolver must trim a trailing slash so URL building does not
// double the separator.
func TestNewVaultwardenResolver_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL + "/")
	if _, err := r.Resolve(context.Background(), "vault://abc"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotPath != "/object/item/abc" {
		t.Fatalf("path = %q, want no double slash", gotPath)
	}
}
