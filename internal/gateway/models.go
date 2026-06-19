package gateway

import (
	"context"
	"fmt"
	"net/http"
)

// ModelManager governs the gateway's model pool + routing (LLD-07). Separate
// from Client (key/team governance) so callers depend only on what they use.
type ModelManager interface {
	// TestConnection verifies the gateway is reachable + authorized.
	TestConnection(ctx context.Context) error
	// NewModel adds (or, via UpdateModel semantics, refreshes) a deployment
	// without restarting the proxy (POST /model/new).
	NewModel(ctx context.Context, spec ModelSpec) error
	// DeleteModel removes a deployment (POST /model/delete).
	DeleteModel(ctx context.Context, modelID string) error
	// UpsertComplexityRouter configures the rule-based difficulty router (the
	// "smart" model): tier -> model alias. Simple→cheap, hard→strong.
	UpsertComplexityRouter(ctx context.Context, spec RouterSpec) error
}

// ModelSpec is one litellm model_list entry (an upstream/alias).
type ModelSpec struct {
	ModelName string   // outward alias, e.g. tier-fast
	Model     string   // litellm_params.model, e.g. openai/qwen-7b
	APIBase   string   // litellm_params.api_base
	APIKey    string   // litellm_params.api_key (resolved secret, in memory only)
	Tags      []string // optional tag-based routing
}

// RouterSpec configures the Complexity Router "smart" model.
type RouterSpec struct {
	ModelName    string            // usually "smart"
	Tiers        map[string]string // SIMPLE/MEDIUM/COMPLEX/REASONING -> model alias
	DefaultModel string            // fallback when no tier matches
}

func (c *HTTPClient) TestConnection(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gateway test connection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gateway test connection: status %d", resp.StatusCode)
	}
	return nil
}

func (c *HTTPClient) NewModel(ctx context.Context, spec ModelSpec) error {
	if spec.ModelName == "" || spec.Model == "" {
		return fmt.Errorf("NewModel: model_name and model are required")
	}
	params := map[string]any{"model": spec.Model}
	if spec.APIBase != "" {
		params["api_base"] = spec.APIBase
	}
	if spec.APIKey != "" {
		params["api_key"] = spec.APIKey
	}
	body := map[string]any{
		"model_name":     spec.ModelName,
		"litellm_params": params,
	}
	if len(spec.Tags) > 0 {
		body["model_info"] = map[string]any{"tags": spec.Tags}
	}
	return c.post(ctx, "/model/new", body, nil)
}

func (c *HTTPClient) DeleteModel(ctx context.Context, modelID string) error {
	if modelID == "" {
		return fmt.Errorf("DeleteModel: id required")
	}
	return c.post(ctx, "/model/delete", map[string]any{"id": modelID}, nil)
}

func (c *HTTPClient) UpsertComplexityRouter(ctx context.Context, spec RouterSpec) error {
	if len(spec.Tiers) == 0 {
		return fmt.Errorf("UpsertComplexityRouter: tiers required")
	}
	name := spec.ModelName
	if name == "" {
		name = "smart"
	}
	cfg := map[string]any{"tiers": spec.Tiers}
	params := map[string]any{
		"model":                    "auto_router/complexity_router",
		"complexity_router_config": cfg,
	}
	if spec.DefaultModel != "" {
		params["complexity_router_default_model"] = spec.DefaultModel
	}
	return c.post(ctx, "/model/new", map[string]any{
		"model_name":     name,
		"litellm_params": params,
	}, nil)
}
