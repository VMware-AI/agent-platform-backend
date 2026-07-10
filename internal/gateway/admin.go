package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// AdminClient wraps HTTPClient with the higher-level "router settings" surface
// called out by the LiteLLM design doc §3.2 — POST /config/update for the
// full router_settings payload, GET /v2/model/info for grouped-model lookups,
// and PATCH /model/{id}/update for partial-update workflows (e.g. one-click
// 熔断). It embeds *HTTPClient so auth/retry/breaker/redactSecrets are reused
// without duplication.
type AdminClient struct {
	*HTTPClient
}

func NewAdminClient(c *HTTPClient) *AdminClient { return &AdminClient{HTTPClient: c} }

// RouterSettings is the body of POST /config/update. Field names follow the
// LiteLLM wire format exactly (snake_case). Only the routing/fallback surface
// is modelled here — circuit-breaker / retry tuning (allowed_fails,
// cooldown_time, num_retries, timeout, retry_after, model_group_retry_policy)
// is intentionally left to LiteLLM's global config.yaml; exposing it in the
// console is out of scope for the design doc's first pass.
type RouterSettings struct {
	RoutingStrategy        string            `json:"routing_strategy,omitempty"`
	RoutingStrategyArgs    map[string]any    `json:"routing_strategy_args,omitempty"`
	RoutingGroups          []RoutingGroup    `json:"routing_groups,omitempty"`
	ModelGroupAlias        map[string]string `json:"model_group_alias,omitempty"`
	Fallbacks              []FallbackEntry   `json:"fallbacks,omitempty"`
	ContextWindowFallbacks []FallbackEntry   `json:"context_window_fallbacks,omitempty"`
	ContentPolicyFallbacks []FallbackEntry   `json:"content_policy_fallbacks,omitempty"`
}

// RoutingGroup is one entry under router_settings.routing_groups — a named
// model group with its own routing strategy override. LiteLLM expects the
// group name to end with "-group" by convention; see design doc §3.2 step 2.
type RoutingGroup struct {
	GroupName           string         `json:"group_name"`
	Models              []string       `json:"models"`
	RoutingStrategy     string         `json:"routing_strategy"`
	RoutingStrategyArgs map[string]any `json:"routing_strategy_args,omitempty"`
}

// FallbackEntry maps an alias to an ordered list of fallback aliases. Each
// Router fallback kind (general / context_window / content_policy) takes the
// same shape but maps to a different router_settings field.
type FallbackEntry map[string][]string

// GroupedModelInfo is the LiteLLM /v2/model/info response shape. Only the
// fields the control plane reads today (id / model_name) are exposed; the
// rest of the payload is preserved as Raw for callers that want to introspect
// without us committing to a wider schema.
type GroupedModelInfo struct {
	Raw json.RawMessage
}

// PushRouterSettings issues POST /config/update with the aggregated router
// settings. The LiteLLM spec marks this endpoint as the atomic "全量聚合
// 覆盖刷新" call: every save must push the full topology, not a delta, so
// data-plane state converges with control-plane state. The doc restricts
// this to admin; the resolver enforces @hasRole(any: [admin]).
func (a *AdminClient) PushRouterSettings(ctx context.Context, s RouterSettings) error {
	body := map[string]any{"router_settings": s}
	return a.post(ctx, "/config/update", body, nil)
}

// GetGroupedModelInfo fetches one model group detail from GET /v2/model/info
// (LiteLLM's grouped-info endpoint, distinct from /models which lists
// deployments). Used by the console route-detail panel.
func (a *AdminClient) GetGroupedModelInfo(ctx context.Context, modelName string) (GroupedModelInfo, error) {
	if modelName == "" {
		return GroupedModelInfo{}, errors.New("GetGroupedModelInfo: modelName required")
	}
	var raw json.RawMessage
	if err := a.get(ctx, "/v2/model/info?model_group="+modelName, &raw); err != nil {
		return GroupedModelInfo{}, err
	}
	return GroupedModelInfo{Raw: raw}, nil
}

// PatchModel issues PATCH /model/{id}/update with the given partial body —
// the standard LiteLLM REST path for single-field changes like toggling
// `blocked`. We use a permissive JSON object (map[string]any) so callers can
// patch any supported model_info / litellm_params field without us having to
// pre-declare a typed wrapper.
func (a *AdminClient) PatchModel(ctx context.Context, modelID string, partial map[string]any) error {
	if modelID == "" {
		return errors.New("PatchModel: modelID required")
	}
	if len(partial) == 0 {
		return errors.New("PatchModel: partial body required")
	}
	return a.patch(ctx, "/model/"+modelID+"/update", partial, nil)
}

// PushModelUpdate issues POST /model/update with the bulk replacement body
// for one model_name group on litellm. Semantics: litellm replaces the entire
// group; specs not in body are deleted by litellm. Callers MUST ensure the
// body IS the final spec set (see plan §R5). Wire shape follows litellm's
// /model/update endpoint with model_name + litellm_params + model_info per
// deployment.
//
// 0.1.x: added for UpdateProviderModel — bulk replaces all specs under a
// ProviderModel in a single round-trip. specID is preserved on each spec
// (model_info.id) so subsequent /model/delete calls hit the right deployment.
func (a *AdminClient) PushModelUpdate(ctx context.Context, providerName string, specs []ModelSpec) error {
	if providerName == "" {
		return errors.New("PushModelUpdate: providerName required")
	}
	if len(specs) == 0 {
		return errors.New("PushModelUpdate: at least one spec required")
	}
	body := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		entry := map[string]any{
			"model_name":     providerName,
			"litellm_params": LitellmParamsFromSpec(s),
		}
		if mi := ModelInfoFromSpec(s); mi != nil {
			entry["model_info"] = mi
		}
		body = append(body, entry)
	}
	return a.post(ctx, "/model/update", body, nil)
}

// AggregateRouterSettings builds a router_settings payload from the DB rows.
// Idempotent: re-running on the same data yields the same JSON. Each route
// becomes one routing_group — the wire shape is:
//
//	routing_groups:
//	  group_name:        <route.name>
//	  models:            <route.supportedModels>
//	  routing_strategy:  <ToLitellmRoutingStrategy(route.strategy)>
//	fallbacks:
//	  <route.supportedModels[0]>: <route.fallbacks>
//	context_window_fallbacks:
//	  <route.supportedModels[0]>: <route.contextWindowFallbacks>
//	content_policy_fallbacks:
//	  <route.supportedModels[0]>: <route.contentPolicyFallbacks>
//
// Routes with an empty SupportedModels are skipped — they would otherwise
// produce a routing_group with no models (litellm would reject the payload
// or produce a no-op group). The console form requires supportedModels on
// create, so empty is a defensive guard only.
//
// tiersByAlias is reserved for a future per-tier routing strategy override;
// LiteLLM's routing_groups don't currently surface tier metadata, so the
// field stays nil on the wire and callers pass nil. The seam is kept so a
// future enhancement can slot in without changing call sites.
func AggregateRouterSettings(activeRoutes []*ent.ModelRoute, tiersByAlias map[string]string) RouterSettings {
	out := RouterSettings{}
	groups := make([]RoutingGroup, 0, len(activeRoutes))
	for _, r := range activeRoutes {
		if r == nil || len(r.SupportedModels) == 0 {
			continue
		}
		groups = append(groups, RoutingGroup{
			GroupName:       r.Name,
			Models:          append([]string(nil), r.SupportedModels...),
			RoutingStrategy: ToLitellmRoutingStrategy(string(r.Strategy)),
		})
	}
	out.RoutingGroups = groups

	if len(tiersByAlias) > 0 {
		out.ModelGroupAlias = make(map[string]string, len(tiersByAlias))
		for alias, tier := range tiersByAlias {
			out.ModelGroupAlias[alias] = tier
		}
	}

	if fb := fallbackEntries(activeRoutes, "fallbacks"); len(fb) > 0 {
		out.Fallbacks = fb
	}
	if fb := fallbackEntries(activeRoutes, "context_window_fallbacks"); len(fb) > 0 {
		out.ContextWindowFallbacks = fb
	}
	if fb := fallbackEntries(activeRoutes, "content_policy_fallbacks"); len(fb) > 0 {
		out.ContentPolicyFallbacks = fb
	}

	return out
}

// fallbackEntries collects the named fallback slice off every route into one
// FallbackEntry keyed by the route's supportedModels[0]. Routes with no
// supportedModels or an empty fallback slice are skipped. The kind
// discriminator is the column name so callers don't repeat themselves.
//
// Key choice: litellm's /config/update wire format keys each fallback entry
// by a model name (not a route id). The route's supportedModels[0] is the
// canonical model name to use — console form validation guarantees it
// matches the target model group.
func fallbackEntries(routes []*ent.ModelRoute, kind string) []FallbackEntry {
	out := make([]FallbackEntry, 0)
	for _, r := range routes {
		if r == nil || len(r.SupportedModels) == 0 {
			continue
		}
		var entries []string
		switch kind {
		case "fallbacks":
			entries = r.Fallbacks
		case "context_window_fallbacks":
			entries = r.ContextWindowFallbacks
		case "content_policy_fallbacks":
			entries = r.ContentPolicyFallbacks
		default:
			return nil
		}
		if len(entries) == 0 {
			continue
		}
		key := r.SupportedModels[0]
		out = append(out, FallbackEntry{key: append([]string(nil), entries...)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ToLitellmRoutingStrategy maps the ent/GraphQL enum value (UPPER_SNAKE) to
// the kebab-case form LiteLLM consumes on /config/update. Unknown values
// fall back to "simple-shuffle" rather than panicking — the worker can keep
// pushing even after a partial config drift, and the failure shows up in
// the gateway admin log if the strategy is actually invalid.
//
// The mapping is intentionally one-directional; reverse mapping belongs in
// the resolver (it's a UI concern).
func ToLitellmRoutingStrategy(enumValue string) string {
	switch strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(enumValue, "-", "_"), " ", "_")) {
	case "SIMPLE_SHUFFLE":
		return "simple-shuffle"
	case "LEAST_BUSY":
		return "least-busy"
	case "LATENCY_BASED_ROUTING":
		return "latency-based-routing"
	case "USAGE_BASED_ROUTING":
		return "usage-based-routing"
	case "USAGE_BASED_ROUTING_V2":
		return "usage-based-routing-v2"
	case "COST_BASED_ROUTING":
		return "cost-based-routing"
	default:
		return "simple-shuffle"
	}
}

// (Design note: a custom MarshalJSON was considered for RouterSettings to
// enforce "routing_strategy required when routing_groups is set", but the
// constraint is best expressed in the resolver that calls this builder, not
// in the wire type. Left here as a forward-compat seam.)
