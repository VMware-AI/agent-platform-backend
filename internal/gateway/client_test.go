package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody GenerateKeyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"sk-abc","expires":"2026-07-19T00:00:00Z","user_id":"u1","spend":0}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	budget := 50.0
	rpm := 60
	resp, err := c.GenerateKey(context.Background(), GenerateKeyRequest{
		UserID: "u1", TeamID: "t1", Models: []string{"smart"},
		MaxBudget: &budget, BudgetDuration: "30d", RPMLimit: &rpm,
		Metadata: map[string]string{"department": "research"},
	})
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if resp.Key != "sk-abc" || resp.UserID != "u1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if gotAuth != "Bearer sk-master" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/key/generate" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody.UserID != "u1" || len(gotBody.Models) != 1 || gotBody.Models[0] != "smart" {
		t.Errorf("request body not sent correctly: %+v", gotBody)
	}
	if gotBody.MaxBudget == nil || *gotBody.MaxBudget != 50.0 {
		t.Errorf("max_budget not sent: %+v", gotBody.MaxBudget)
	}
}

func TestDeleteKey_RequiresKey(t *testing.T) {
	c, _ := NewHTTPClient("http://unused", "sk-master")
	if err := c.DeleteKey(context.Background(), ""); err == nil {
		t.Fatal("DeleteKey with empty key should error")
	}
}

func TestPost_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-bad")
	if _, err := c.GenerateKey(context.Background(), GenerateKeyRequest{UserID: "u1"}); err == nil {
		t.Fatal("expected error on 401 status")
	}
}

func TestDeleteTeam(t *testing.T) {
	var gotPath string
	var gotBody map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-master")
	if err := c.DeleteTeam(context.Background(), "t-research"); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if gotPath != "/team/delete" {
		t.Errorf("path = %q", gotPath)
	}
	if ids := gotBody["team_ids"]; len(ids) != 1 || ids[0] != "t-research" {
		t.Errorf("team_ids not sent: %+v", gotBody)
	}
}

func TestDeleteTeam_RequiresTeamID(t *testing.T) {
	c, _ := NewHTTPClient("http://unused", "sk-master")
	if err := c.DeleteTeam(context.Background(), ""); err == nil {
		t.Fatal("DeleteTeam with empty id should error")
	}
}

func TestUpdateKey_Blocked(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-master")
	blocked := true
	if err := c.UpdateKey(context.Background(), UpdateKeyRequest{Key: "sk-x", Blocked: &blocked}); err != nil {
		t.Fatalf("UpdateKey: %v", err)
	}
	if gotPath != "/key/update" {
		t.Errorf("path = %q, want /key/update", gotPath)
	}
	if gotBody["key"] != "sk-x" {
		t.Errorf("key not sent: %+v", gotBody)
	}
	if b, ok := gotBody["blocked"].(bool); !ok || !b {
		t.Errorf("blocked=true not sent: %+v", gotBody)
	}
}

func TestUpdateKey_RequiresKey(t *testing.T) {
	c, _ := NewHTTPClient("http://unused", "sk-master")
	if err := c.UpdateKey(context.Background(), UpdateKeyRequest{}); err == nil {
		t.Fatal("UpdateKey with empty key should error")
	}
}

func TestGet_RetriesOnServerError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // transient 5xx
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master", WithRetryBackoff(0))
	if _, err := c.ListTeams(context.Background()); err != nil {
		t.Fatalf("ListTeams should succeed after retries: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("want 3 attempts (2 retries), got %d", got)
	}
}

func TestGet_NoRetryOn4xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest) // client error — retrying won't help
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master", WithRetryBackoff(0))
	if _, err := c.ListTeams(context.Background()); err == nil {
		t.Fatal("expected error on 400")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("4xx must not be retried, got %d attempts", got)
	}
}

// Mutations must be exactly-once: a 5xx on POST is NOT retried (the server may
// have already applied it).
func TestPost_NoRetry(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master", WithRetryBackoff(0))
	if _, err := c.GenerateKey(context.Background(), GenerateKeyRequest{UserID: "u1"}); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("POST must not be retried, got %d attempts", got)
	}
}

func TestRegenerateKey(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"sk-new","user_id":"u1","spend":0}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	resp, err := c.RegenerateKey(context.Background(), "sk-old")
	if err != nil {
		t.Fatalf("RegenerateKey: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/key/sk-old/regenerate" {
		t.Errorf("want POST /key/sk-old/regenerate, got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-master" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if resp.Key != "sk-new" {
		t.Errorf("new key = %q, want sk-new", resp.Key)
	}
}

func TestRegenerateKey_RequiresKey(t *testing.T) {
	c, _ := NewHTTPClient("http://unused", "sk-master")
	if _, err := c.RegenerateKey(context.Background(), ""); err == nil {
		t.Fatal("RegenerateKey with empty key should error")
	}
}

func TestListTeams(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"team_id":"t1","team_alias":"research"},
			{"team_id":"t2"},
			{"team_alias":"no-id-skip-me"}
		]`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	teams, err := c.ListTeams(context.Background())
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/team/list" {
		t.Errorf("want GET /team/list, got %s %s", gotMethod, gotPath)
	}
	if len(teams) != 2 { // the id-less entry is skipped
		t.Fatalf("want 2 teams, got %d: %+v", len(teams), teams)
	}
	if teams[0].TeamID != "t1" || teams[0].Alias != "research" {
		t.Errorf("teams[0] = %+v", teams[0])
	}
	if teams[1].TeamID != "t2" {
		t.Errorf("teams[1] = %+v", teams[1])
	}
}

func TestListKeys(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[
			{"token":"hash1","user_id":"u1","team_id":"t1"},
			{"key":"sk-raw2","user_id":"u2"}
		]}`))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	keys, err := c.ListKeys(context.Background())
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/key/list" {
		t.Errorf("want GET /key/list, got %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-master" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d: %+v", len(keys), keys)
	}
	// token is used as the identifier when no raw key is present; raw key wins when given.
	if keys[0].Key != "hash1" || keys[0].UserID != "u1" || keys[0].TeamID != "t1" {
		t.Errorf("keys[0] = %+v", keys[0])
	}
	if keys[1].Key != "sk-raw2" || keys[1].UserID != "u2" {
		t.Errorf("keys[1] = %+v", keys[1])
	}
}

func TestCreateTeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/team/new" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"team_id":"t-research"}`))
	}))
	defer srv.Close()
	c, _ := NewHTTPClient(srv.URL, "sk-master")
	budget := 500.0
	resp, err := c.CreateTeam(context.Background(), TeamRequest{TeamAlias: "research", MaxBudget: &budget})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if resp.TeamID != "t-research" {
		t.Fatalf("unexpected team: %+v", resp)
	}
}
