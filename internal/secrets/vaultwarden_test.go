package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// --- APIKey round-trip (gateway master-key onboarding path) ---
//
// A standalone master key (Credential{APIKey: "sk-…"}) is stored as a Bitwarden
// custom field named "api_key" on the login item, and Resolve reads it back.
// Without the custom-field write, Resolve would return an empty APIKey and every
// downstream litellm call would 401 with "Malformed API Key" (a master-key
// Auth header of "Bearer " with no token). This is the regression test for
// that exact bug.

func TestVaultwardenResolver_Put_APIKeyGoesToCustomField(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":"k-1"}}`)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	if _, err := r.Put(context.Background(), "gateway/primary",
		Credential{APIKey: "sk-local-abc123"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.Contains(gotBody, `"name":"api_key"`) ||
		!strings.Contains(gotBody, `"value":"sk-local-abc123"`) {
		t.Fatalf("Put body missing api_key custom field: %s", gotBody)
	}
}

func TestVaultwardenResolver_Put_OmitsFieldsWhenAPIKeyEmpty(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":"u-1"}}`)
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	if _, err := r.Put(context.Background(), "agent-ui/vm",
		Credential{Username: "agent", Password: "p@ss"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if strings.Contains(gotBody, `"fields"`) {
		t.Fatalf("Put must not emit fields when APIKey is empty, got: %s", gotBody)
	}
}

func TestVaultwardenResolver_PutThenResolve_APIKeyRoundTrip(t *testing.T) {
	// Minimal in-memory bw serve: stores one item, returns the canonical item
	// shape Resolve expects. Verifies the Put payload includes the api_key
	// custom field and that Resolve reads it back into Credential.APIKey.
	var mu sync.Mutex
	var stored struct {
		Username  string   `json:"username"`
		Password  string   `json:"password"`
		FieldName string   `json:"name"`
		FieldVal  string   `json:"value"`
		APIKeySet bool
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/object/item":
			var p struct {
				Login struct {
					Username string `json:"username"`
					Password string `json:"password"`
				} `json:"login"`
				Fields []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			stored.Username = p.Login.Username
			stored.Password = p.Login.Password
			for _, f := range p.Fields {
				if f.Name == "api_key" {
					stored.FieldName = f.Name
					stored.FieldVal = f.Value
					stored.APIKeySet = true
				}
			}
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":"id-1"}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/object/item/"):
			mu.Lock()
			fields := []map[string]string{}
			if stored.APIKeySet {
				fields = append(fields, map[string]string{
					"name": stored.FieldName, "value": stored.FieldVal,
				})
			}
			mu.Unlock()
			// Encode the item as bw serve would.
			out := map[string]any{
				"success": true,
				"data": map[string]any{
					"login":  map[string]string{"username": stored.Username, "password": stored.Password},
					"fields": fields,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	r := NewVaultwardenResolver(srv.URL)
	ref, err := r.Put(context.Background(), "gateway/primary",
		Credential{APIKey: "sk-local-roundtrip"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref != "vault://id-1" {
		t.Fatalf("ref = %q, want vault://id-1", ref)
	}

	cred, err := r.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.APIKey != "sk-local-roundtrip" {
		t.Fatalf("round-trip APIKey = %q, want %q", cred.APIKey, "sk-local-roundtrip")
	}
}
