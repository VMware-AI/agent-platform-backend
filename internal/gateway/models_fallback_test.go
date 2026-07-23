package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsFallsBackToV1Models(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/models":
			http.NotFound(w, r)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek","model_name":"deepseek"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "deepseek" {
		t.Fatalf("models = %+v, want deepseek", models)
	}
	if len(paths) != 2 || paths[0] != "/models" || paths[1] != "/v1/models" {
		t.Fatalf("paths = %v, want [/models /v1/models]", paths)
	}
}

func TestTestConnectionFallsBackToV1Models(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/models":
			http.NotFound(w, r)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	if err := client.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/models" || paths[1] != "/v1/models" {
		t.Fatalf("paths = %v, want [/models /v1/models]", paths)
	}
}

func TestListOllamaTags(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:3b"},{"name":"deepseek-r1:7b"}]}`))
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, "ollama")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	models, err := client.ListOllamaTags(context.Background())
	if err != nil {
		t.Fatalf("ListOllamaTags: %v", err)
	}
	if len(models) != 2 || models[0].ID != "qwen2.5-coder:3b" || models[1].ModelName != "deepseek-r1:7b" {
		t.Fatalf("models = %+v, want Ollama tag names", models)
	}
	if len(paths) != 1 || paths[0] != "/api/tags" {
		t.Fatalf("paths = %v, want [/api/tags]", paths)
	}
}
