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
	// routing strategy (GET /config/router). Best-effort: callers should treat
	// any error (transport, 5xx, or ErrUnknownRoutingStrategy) as "unknown" and
	// not fail the broader operation.
	GetRoutingStrategy(ctx context.Context) (RoutingStrategy, error)
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
	// ModelID pins litellm's model_info.id to a caller-owned id (we use the Upstream
	// row id) so the deployment can later be deleted deterministically via
	// /model/delete {"id": ...}. Verified against a real litellm: it honors a custom
	// model_info.id and delete-by-that-id succeeds. Empty → litellm assigns its own.
	ModelID string
}

// DefaultRouterModel is the default alias of the Complexity Router model.
const DefaultRouterModel = "smart"

// RoutingStrategy is the global load-balancing strategy that litellm is currently
// configured to use (the "routing_strategy" field of /config/router). The string
// values are the literal names litellm uses on the wire; the console maps them to
// the wider LoadBalancingStrategy enum exposed via GraphQL.
type RoutingStrategy string

const (
	RoutingStrategyRoundRobin   RoutingStrategy = "simple_shuffle"
	RoutingStrategyLatencyBased RoutingStrategy = "latency"
	RoutingStrategyUsageBasedV2 RoutingStrategy = "usage_v2"
	RoutingStrategyLeastBusy    RoutingStrategy = "least_busy"
	RoutingStrategyCostBased    RoutingStrategy = "cost"
)

// ErrUnknownRoutingStrategy is returned by GetRoutingStrategy when the litellm
// version reports a routing_strategy value we don't recognise. Callers should log
// and treat the field as "unknown" rather than failing the broader operation —
// the strategy is best-effort metadata, not a hard contract.
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

// GetRoutingStrategy calls GET /config/router and maps the response's
// "routing_strategy" field to a RoutingStrategy. Transport errors, 5xx (after
// retry), and 4xx all return their underlying error verbatim; unmapped wire
// values return ErrUnknownRoutingStrategy so callers can decide to treat the
// field as "unknown" rather than fail the broader operation.
func (c *HTTPClient) GetRoutingStrategy(ctx context.Context) (RoutingStrategy, error) {
	var out struct {
		RoutingStrategy string `json:"routing_strategy"`
	}
	if err := c.get(ctx, "/config/router", &out); err != nil {
		return "", err
	}
	switch RoutingStrategy(out.RoutingStrategy) {
	case RoutingStrategyRoundRobin,
		RoutingStrategyLatencyBased,
		RoutingStrategyUsageBasedV2,
		RoutingStrategyLeastBusy,
		RoutingStrategyCostBased:
		return RoutingStrategy(out.RoutingStrategy), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownRoutingStrategy, out.RoutingStrategy)
	}
}
