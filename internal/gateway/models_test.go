package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewModel(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, "sk-master")

	err := c.NewModel(context.Background(), ModelSpec{
		ModelName: "tier-fast", Model: "openai/qwen-7b", APIBase: "http://vllm:8000", APIKey: "sk-up",
	})
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}
	if gotPath != "/model/new" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["model_name"] != "tier-fast" {
		t.Fatalf("model_name not sent: %+v", gotBody)
	}
	params, _ := gotBody["litellm_params"].(map[string]any)
	if params["model"] != "openai/qwen-7b" || params["api_base"] != "http://vllm:8000" {
		t.Fatalf("litellm_params wrong: %+v", params)
	}
}

func TestUpsertComplexityRouter(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, "sk-master")

	err := c.UpsertComplexityRouter(context.Background(), RouterSpec{
		ModelName: "smart",
		Tiers: map[string]string{
			"SIMPLE": "tier-fast", "MEDIUM": "tier-mid", "COMPLEX": "tier-heavy", "REASONING": "tier-reason",
		},
		DefaultModel: "tier-mid",
	})
	if err != nil {
		t.Fatalf("UpsertComplexityRouter: %v", err)
	}
	if gotBody["model_name"] != "smart" {
		t.Fatalf("model_name = %v", gotBody["model_name"])
	}
	params, _ := gotBody["litellm_params"].(map[string]any)
	if params["model"] != "auto_router/complexity_router" {
		t.Fatalf("router model wrong: %+v", params)
	}
	cfg, _ := params["complexity_router_config"].(map[string]any)
	tiers, _ := cfg["tiers"].(map[string]any)
	if tiers["SIMPLE"] != "tier-fast" || tiers["REASONING"] != "tier-reason" {
		t.Fatalf("tiers not sent correctly: %+v", tiers)
	}
}

func TestTestConnection(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer sk-master" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer ok.Close()
	if err := NewHTTPClient(ok.URL, "sk-master").TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection should succeed: %v", err)
	}
	if err := NewHTTPClient(ok.URL, "wrong").TestConnection(context.Background()); err == nil {
		t.Fatal("bad key should fail TestConnection")
	}
}
