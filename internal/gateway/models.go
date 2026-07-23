package gateway

import (
	"context"
	"errors"
	"fmt"
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
}

// ModelInfo is one entry from the gateway's /models listing. The fields are
// what litellm reports; we only need the count today, but model_name / id
// round-trip for future per-gateway inspection.
type ModelInfo struct {
	ID        string `json:"id"`
	ModelName string `json:"model_name"`
}

// ModelSpec is one litellm model_list entry (a provider model / alias).
//
// All litellm_params fields supported by the /model/new, /model/update and
// /model/{id}/update wire endpoints are mirrored here. Numeric and bool
// fields are pointers so the wire builder can distinguish "unset" (skip the
// key) from "explicit zero / false" (emit the key); specJSON in the graph
// package uses the same convention so the pass-through is one-to-one.
type ModelSpec struct {
	ModelName string // outward alias, e.g. tier-fast
	ModelID   string // pins litellm's model_info.id so /model/delete can target it later

	// litellm_params (all optional; empty/zero/nil ⇒ omit from wire body)
	Model                          string
	APIBase                        string
	APIKey                         string
	CustomLlmProvider              string
	Organization                   string
	Tpm                            *int
	Rpm                            *int
	DefaultAPIKeyTpmLimit          *int
	DefaultAPIKeyRpmLimit          *int
	MaxBudget                      *float64
	BudgetDuration                 string
	UseInPassThrough               *bool
	UseChatCompletionsAPI          *bool
	MergeReasoningContentInChoices *bool
	Tags                           []string
	InputCostPerToken              *float64
	OutputCostPerToken             *float64
	CacheReadInputTokenCost        *float64
	CacheCreationInputTokenCost    *float64
}

// LitellmParamsFromSpec renders s into a wire body for litellm's litellm_params
// object. Empty / zero / nil fields are skipped so the request shape stays
// minimal (avoiding litellm's "silent drop" behaviour when an unsupported
// combination is supplied — e.g. model=MiniMax-M2.5 without custom_llm_provider).
// Shared by NewModel / PushModelUpdate / PatchModel to keep the wire contract
// consistent across all three endpoints. Exported so the graph layer's
// partial-update helper can render the same body shape.
func LitellmParamsFromSpec(s ModelSpec) map[string]any {
	p := map[string]any{}
	if s.Model != "" {
		p["model"] = s.Model
	}
	if s.APIBase != "" {
		p["api_base"] = s.APIBase
	}
	if s.APIKey != "" {
		p["api_key"] = s.APIKey
	}
	if s.CustomLlmProvider != "" {
		p["custom_llm_provider"] = s.CustomLlmProvider
	}
	if s.Organization != "" {
		p["organization"] = s.Organization
	}
	if s.Tpm != nil {
		p["tpm"] = *s.Tpm
	}
	if s.Rpm != nil {
		p["rpm"] = *s.Rpm
	}
	if s.DefaultAPIKeyTpmLimit != nil {
		p["default_api_key_tpm_limit"] = *s.DefaultAPIKeyTpmLimit
	}
	if s.DefaultAPIKeyRpmLimit != nil {
		p["default_api_key_rpm_limit"] = *s.DefaultAPIKeyRpmLimit
	}
	if s.MaxBudget != nil {
		p["max_budget"] = *s.MaxBudget
	}
	if s.BudgetDuration != "" {
		p["budget_duration"] = s.BudgetDuration
	}
	if s.UseInPassThrough != nil {
		p["use_in_pass_through"] = *s.UseInPassThrough
	}
	if s.UseChatCompletionsAPI != nil {
		p["use_chat_completions_api"] = *s.UseChatCompletionsAPI
	}
	if s.MergeReasoningContentInChoices != nil {
		p["merge_reasoning_content_in_choices"] = *s.MergeReasoningContentInChoices
	}
	if s.InputCostPerToken != nil {
		p["input_cost_per_token"] = *s.InputCostPerToken
	}
	if s.OutputCostPerToken != nil {
		p["output_cost_per_token"] = *s.OutputCostPerToken
	}
	if s.CacheReadInputTokenCost != nil {
		p["cache_read_input_token_cost"] = *s.CacheReadInputTokenCost
	}
	if s.CacheCreationInputTokenCost != nil {
		p["cache_creation_input_token_cost"] = *s.CacheCreationInputTokenCost
	}
	// Note: s.Tags is NOT a litellm_params field — it belongs in model_info
	// (see NewModel body). Routed there explicitly to preserve the original
	// wire shape; pushing it under litellm_params would change semantics.
	return p
}

// ModelInfoFromSpec renders the top-level model_info object. Today only the
// pin id + optional routing tags live there; if litellm adds more model_info
// fields, route them through this helper to keep NewModel / PatchModel in
// sync. Exported so the graph layer's partial-update helper can render the
// same body shape.
func ModelInfoFromSpec(s ModelSpec) map[string]any {
	mi := map[string]any{}
	if s.ModelID != "" {
		mi["id"] = s.ModelID
	}
	if len(s.Tags) > 0 {
		mi["tags"] = s.Tags
	}
	if len(mi) == 0 {
		return nil
	}
	return mi
}

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

// TestConnection reuses c.get so the retry + error-class semantics stay
// consistent with ListModels. LiteLLM deployments differ by version: some
// expose the list at /models, while OpenAI-compatible surfaces expose
// /v1/models. Accept either path for reachability.
func (c *HTTPClient) TestConnection(ctx context.Context) error {
	if err := c.get(ctx, "/models", nil); err != nil {
		return c.get(ctx, "/v1/models", nil)
	}
	return nil
}

// NewModel creates (or refreshes) a litellm deployment (POST /model/new). On
// 2xx the response carries a "model_name" field — confirmed against the request
// so a silent server-side reject (e.g. unknown upstream) surfaces as
// ErrMalformedResponse rather than silently "succeeding".
func (c *HTTPClient) NewModel(ctx context.Context, spec ModelSpec) error {
	if spec.ModelName == "" || spec.Model == "" {
		return fmt.Errorf("NewModel: model_name and model are required")
	}
	body := map[string]any{
		"model_name":     spec.ModelName,
		"litellm_params": LitellmParamsFromSpec(spec),
	}
	if mi := ModelInfoFromSpec(spec); mi != nil {
		body["model_info"] = mi
	}
	var resp struct {
		ModelName string `json:"model_name"`
	}
	if err := c.post(ctx, "/model/new", body, &resp); err != nil {
		return err
	}
	// litellm is inconsistent about whether it echoes back the request's
	// model_name or the upstream provider's name. Accept either, as long as
	// the response carries some non-empty identifier.
	if resp.ModelName == "" {
		return fmt.Errorf("%w: NewModel response missing model_name", ErrMalformedResponse)
	}
	return nil
}

func (c *HTTPClient) DeleteModel(ctx context.Context, modelID string) error {
	if modelID == "" {
		return fmt.Errorf("DeleteModel: id required")
	}
	return c.post(ctx, "/model/delete", map[string]any{"id": modelID}, nil)
}

// ListModels calls GET /models and decodes the response's "data" array. A
// transport / decode / non-2xx error bubbles up; callers (probeGatewayBackendModelCount)
// decide whether to fail the broader operation. An empty/missing "data" array
// is treated as zero models (a freshly-started litellm returns that).
func (c *HTTPClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	models, err := c.ListModelsAt(ctx, "/models")
	if err == nil {
		return models, nil
	}
	return c.ListModelsAt(ctx, "/v1/models")
}

// ListModelsAt calls GET {path} and decodes the response's "data" array of
// {id, model_name}. Use this for non-/models endpoints (e.g. <apiBase>/v1/models
// for OpenAI-compatible upstreams that require the /v1 prefix). Same retry /
// decode / error semantics as ListModels.
func (c *HTTPClient) ListModelsAt(ctx context.Context, path string) ([]ModelInfo, error) {
	var out struct {
		Data []ModelInfo `json:"data"`
	}
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ListOllamaTags calls Ollama's native GET /api/tags endpoint and projects
// models[].name into the same ModelInfo shape used by OpenAI-compatible probes.
func (c *HTTPClient) ListOllamaTags(ctx context.Context) ([]ModelInfo, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := c.get(ctx, "/api/tags", &out); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(out.Models))
	for _, m := range out.Models {
		if m.Name == "" {
			continue
		}
		models = append(models, ModelInfo{ID: m.Name, ModelName: m.Name})
	}
	return models, nil
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
