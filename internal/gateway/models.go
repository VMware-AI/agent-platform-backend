package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ModelManager governs the gateway's model pool + routing (LLD-07). Separate
// from Client (key/team governance) so callers depend only on what they use.
type ModelManager interface {
	// TestConnection verifies the gateway is reachable + authorized.
	TestConnection(ctx context.Context) error
	// GetRoutingStrategy returns the gateway's currently-configured global
	// routing strategy (the `routing_strategy` field of `current_values` in
	// GET /router/settings). Best-effort: callers should treat any error
	// (transport, 5xx, or ErrUnknownRoutingStrategy) as "unknown" and not
	// fail the broader operation.
	GetRoutingStrategy(ctx context.Context) (RoutingStrategy, error)
	// ListModels returns the model deployments the gateway currently holds
	// (GET /models). The result is what the gateway actually serves — distinct
	// from any local registry of intended deployments, so it can include
	// operator-pushed models and exclude locally-tracked ones that haven't been
	// pushed yet. Best-effort metadata: callers should treat any error as
	// "unknown" and not fail the broader operation.
	ListModels(ctx context.Context) ([]ModelInfo, error)
	// NewModel adds (or, via UpdateModel semantics, refreshes) a deployment
	// without restarting the proxy (POST /model/new).
	NewModel(ctx context.Context, spec ModelSpec) error
	// DeleteModel removes a deployment (POST /model/delete).
	DeleteModel(ctx context.Context, modelID string) error
	// UpsertComplexityRouter configures the rule-based difficulty router (the
	// "smart" model): tier -> model alias. Simple→cheap, hard→strong.
	UpsertComplexityRouter(ctx context.Context, spec RouterSpec) error
}

// ModelInfo is one entry from the gateway's /models listing. The fields are
// what litellm reports; we only need the count today, but model_name / id
// round-trip for future per-gateway inspection.
type ModelInfo struct {
	ID        string `json:"id"`
	ModelName string `json:"model_name"`
}

// ModelSpec is one litellm model_list entry (an upstream/alias).
type ModelSpec struct {
	ModelName string   // outward alias, e.g. tier-fast
	Model     string   // litellm_params.model, e.g. openai/qwen-7b
	APIBase   string   // litellm_params.api_base
	APIKey    string   // litellm_params.api_key (resolved secret, in memory only)
	Tags      []string // optional tag-based routing
	// ModelID pins litellm's model_info.id to a caller-owned id (we use the Upstream
	// row id) so the deployment can later be deleted deterministically via
	// /model/delete {"id": ...}. Verified against a real litellm: it honors a custom
	// model_info.id and delete-by-that-id succeeds. Empty → litellm assigns its own.
	ModelID string
}

// DefaultRouterModel is the default alias of the Complexity Router model.
const DefaultRouterModel = "smart"

// RoutingStrategy is the global load-balancing strategy that litellm is currently
// configured to use (the `routing_strategy` field of the `current_values` block
// in GET /router/settings). The string values are the literal names litellm
// uses on the wire — kebab-case (`simple-shuffle`, `least-busy`,
// `latency-based-routing`, `usage-based-routing`, `usage-based-routing-v2`,
// `cost-based-routing`). The GraphQL `LoadBalancingStrategy` enum
// (UPPER_SNAKE_CASE) and the ent column both share those values after
// translation in internal/graph.mapRoutingStrategy; this type is just the
// inbound wire view.
type RoutingStrategy string

// ErrUnknownRoutingStrategy is returned by GetRoutingStrategy when the litellm
// version reports a routing_strategy value we don't recognise, or when the
// `current_values.routing_strategy` field is absent (older litellm versions
// that don't expose /router/settings at all). Callers should log and treat the
// field as "unknown" rather than failing the broader operation — the strategy
// is best-effort metadata, not a hard contract.
var ErrUnknownRoutingStrategy = errors.New("gateway: unknown routing strategy")

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
	modelInfo := map[string]any{}
	if spec.ModelID != "" {
		modelInfo["id"] = spec.ModelID // pin the id so DeleteModel can target it later
	}
	if len(spec.Tags) > 0 {
		modelInfo["tags"] = spec.Tags
	}
	if len(modelInfo) > 0 {
		body["model_info"] = modelInfo
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
		name = DefaultRouterModel
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

// ListModels calls GET /models and decodes the response's "data" array. A
// transport / decode / non-2xx error bubbles up; callers (probeGatewayBackendModelCount)
// decide whether to fail the broader operation. An empty/missing "data" array
// is treated as zero models (a freshly-started litellm returns that).
func (c *HTTPClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var out struct {
		Data []ModelInfo `json:"data"`
	}
	if err := c.get(ctx, "/models", &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetRoutingStrategy calls GET /router/settings and reads the routing_strategy
// from the response's `current_values` block. The wire value is the kebab-case
// name litellm uses (`simple-shuffle`, `least-busy`, `latency-based-routing`,
// `usage-based-routing`, `usage-based-routing-v2`, `cost-based-routing`).
// `usage-based-routing` is the deprecated pre-v2 form still emitted by some
// litellm versions and is accepted as the same strategy as
// `usage-based-routing-v2` (the strategy probe only differentiates by name).
// Transport errors, 5xx (after retry), and 4xx all return their underlying
// error verbatim; an absent routing_strategy in current_values returns
// ErrUnknownRoutingStrategy so callers can treat the field as "unknown"
// rather than fail the broader operation.
func (c *HTTPClient) GetRoutingStrategy(ctx context.Context) (RoutingStrategy, error) {
	var out struct {
		CurrentValues struct {
			RoutingStrategy string `json:"routing_strategy"`
		} `json:"current_values"`
	}
	if err := c.get(ctx, "/router/settings", &out); err != nil {
		return "", err
	}
	rs := out.CurrentValues.RoutingStrategy
	if rs == "" {
		return "", fmt.Errorf("%w: not present in /router/settings current_values", ErrUnknownRoutingStrategy)
	}
	switch RoutingStrategy(rs) {
	case "simple-shuffle", "least-busy", "latency-based-routing",
		"usage-based-routing", "usage-based-routing-v2", "cost-based-routing":
		return RoutingStrategy(rs), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRoutingStrategy, rs)
	}
}
