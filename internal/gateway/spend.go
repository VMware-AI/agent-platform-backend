package gateway

import (
	"context"
	"fmt"
	"net/url"
)

// SpendReader reads litellm's spend/budget endpoints. It is intentionally a
// SEPARATE interface from Client (governance ops): the observability aggregator
// only needs reads, and keeping it separate means mocks/tests for one don't have
// to implement the other. *HTTPClient satisfies both.
type SpendReader interface {
	// GlobalSpendReport pulls GET /global/spend/report for [start,end] (YYYY-MM-DD)
	// grouped by litellmGroupBy ("team" is the richest — its per-day teams[] carry
	// a metadata[] breakdown by model + api_key, which the aggregator re-groups).
	GlobalSpendReport(ctx context.Context, start, end, litellmGroupBy string) ([]SpendReportDay, error)
	// BudgetInfo reads GET /team/info | /user/info | /key/info for one id.
	BudgetInfo(ctx context.Context, scope BudgetInfoScope, id string) (*BudgetInfo, error)
	// Health reads GET /health (litellm probes its upstreams) + a readiness
	// check, returning the reachable flag and healthy/unhealthy endpoint lists.
	Health(ctx context.Context) (*GatewayHealth, error)
}

// GatewayHealth is one gateway's upstream health, normalized from litellm's
// /health response (+ /health/readiness for reachability).
type GatewayHealth struct {
	Reachable      bool
	HealthyCount   int
	UnhealthyCount int
	Healthy        []EndpointHealth
	Unhealthy      []EndpointHealth
}

// EndpointHealth is one upstream deployment's health.
type EndpointHealth struct {
	Model   string
	APIBase string
}

type llmHealth struct {
	HealthyEndpoints   []llmEndpoint `json:"healthy_endpoints"`
	UnhealthyEndpoints []llmEndpoint `json:"unhealthy_endpoints"`
	HealthyCount       int           `json:"healthy_count"`
	UnhealthyCount     int           `json:"unhealthy_count"`
}

type llmEndpoint struct {
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
}

func (c *HTTPClient) Health(ctx context.Context) (*GatewayHealth, error) {
	var raw llmHealth
	if err := c.get(ctx, "/health", &raw); err != nil {
		return nil, err
	}
	out := &GatewayHealth{
		Reachable:      true, // a successful /health means the proxy answered
		HealthyCount:   raw.HealthyCount,
		UnhealthyCount: raw.UnhealthyCount,
	}
	for _, e := range raw.HealthyEndpoints {
		out.Healthy = append(out.Healthy, EndpointHealth{Model: e.Model, APIBase: e.APIBase})
	}
	for _, e := range raw.UnhealthyEndpoints {
		out.Unhealthy = append(out.Unhealthy, EndpointHealth{Model: e.Model, APIBase: e.APIBase})
	}
	// litellm may omit the counts; derive from the lists when zero.
	if out.HealthyCount == 0 {
		out.HealthyCount = len(out.Healthy)
	}
	if out.UnhealthyCount == 0 {
		out.UnhealthyCount = len(out.Unhealthy)
	}
	return out, nil
}

// BudgetInfoScope selects which litellm *_info endpoint to hit.
type BudgetInfoScope string

const (
	BudgetScopeTeam BudgetInfoScope = "team"
	BudgetScopeUser BudgetInfoScope = "user"
	BudgetScopeKey  BudgetInfoScope = "key"
)

// --- normalized shapes returned to the aggregator ---

// SpendReportDay is one calendar day of spend, normalized from litellm's
// /global/spend/report response (which nests teams[] → metadata[]).
type SpendReportDay struct {
	Date  string      // YYYY-MM-DD
	Teams []TeamSpend // one entry per team_id that had spend that day
}

// TeamSpend is a team's spend on a day, with its per-model/per-key breakdown.
type TeamSpend struct {
	ID     string
	Name   string
	Spend  float64
	Models []ModelSpend // litellm's metadata[]: the breakdown by model (+ api_key)
}

// ModelSpend is one (model, api_key) slice of a team's daily spend.
type ModelSpend struct {
	Model            string
	APIKey           string
	Spend            float64
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// BudgetInfo is the budget slice of a litellm team/user/key info response.
type BudgetInfo struct {
	ID            string
	Label         string
	Spend         float64
	MaxBudget     *float64
	BudgetResetAt *string // litellm returns an ISO timestamp; kept as-is, parsed at the edge
	// LastActiveAt is set by /key/info responses only; nil for team/user
	// scopes. The virtualkey-spend worker reads this to populate
	// VirtualKey.last_active_at. Stored as a pointer so the absence case
	// (older litellm versions that don't emit the field) is unambiguous.
	LastActiveAt *string
}

// --- litellm wire DTOs (best-effort mapping; see LLD-15 §3.2 on version drift) ---

type llmSpendReportDay struct {
	GroupByDay string         `json:"group_by_day"`
	Teams      []llmTeamSpend `json:"teams"`
}

type llmTeamSpend struct {
	TeamID     string          `json:"team_id"`
	TeamName   string          `json:"team_name"`
	TotalSpend float64         `json:"total_spend"`
	Metadata   []llmModelSpend `json:"metadata"`
}

type llmModelSpend struct {
	Model            string  `json:"model"`
	APIKey           string  `json:"api_key"`
	Spend            float64 `json:"spend"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
}

type llmBudgetInfo struct {
	// team/user/key info responses put the budget fields at the top level under
	// slightly different id keys; we read all and pick the non-empty one.
	TeamID        string   `json:"team_id"`
	UserID        string   `json:"user_id"`
	KeyName       string   `json:"key_name"`
	KeyAlias      string   `json:"key_alias"`
	TeamAlias     string   `json:"team_alias"`
	Spend         float64  `json:"spend"`
	MaxBudget     *float64 `json:"max_budget"`
	BudgetResetAt *string  `json:"budget_reset_at"`
	// Last-active timestamp — only /key/info populates this. We carry it
	// through here (omitempty so the team/user scopes don't see a noise
	// field in their JSON) and read it in the spend worker below.
	LastActiveAt *string `json:"last_active_at,omitempty"`
}

func (c *HTTPClient) GlobalSpendReport(ctx context.Context, start, end, litellmGroupBy string) ([]SpendReportDay, error) {
	q := url.Values{}
	q.Set("start_date", start)
	q.Set("end_date", end)
	if litellmGroupBy != "" {
		q.Set("group_by", litellmGroupBy)
	}
	var raw []llmSpendReportDay
	if err := c.get(ctx, "/global/spend/report?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]SpendReportDay, 0, len(raw))
	for _, day := range raw {
		d := SpendReportDay{Date: day.GroupByDay, Teams: make([]TeamSpend, 0, len(day.Teams))}
		for _, t := range day.Teams {
			ts := TeamSpend{ID: t.TeamID, Name: t.TeamName, Spend: t.TotalSpend}
			for _, m := range t.Metadata {
				ts.Models = append(ts.Models, ModelSpend{
					Model:            m.Model,
					APIKey:           m.APIKey,
					Spend:            m.Spend,
					PromptTokens:     m.PromptTokens,
					CompletionTokens: m.CompletionTokens,
					TotalTokens:      m.TotalTokens,
				})
			}
			d.Teams = append(d.Teams, ts)
		}
		out = append(out, d)
	}
	return out, nil
}

func (c *HTTPClient) BudgetInfo(ctx context.Context, scope BudgetInfoScope, id string) (*BudgetInfo, error) {
	q := url.Values{}
	var path string
	switch scope {
	case BudgetScopeTeam:
		path = "/team/info"
		q.Set("team_id", id)
	case BudgetScopeUser:
		path = "/user/info"
		q.Set("user_id", id)
	case BudgetScopeKey:
		path = "/key/info"
		q.Set("key", id)
	default:
		return nil, fmt.Errorf("gateway: unknown budget scope %q", scope)
	}
	var raw llmBudgetInfo
	if err := c.get(ctx, path+"?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	label := firstNonEmpty(raw.TeamAlias, raw.KeyAlias, raw.KeyName, raw.TeamID, raw.UserID, id)
	out := &BudgetInfo{
		ID:            id,
		Label:         label,
		Spend:         raw.Spend,
		MaxBudget:     raw.MaxBudget,
		BudgetResetAt: raw.BudgetResetAt,
	}
	// Only /key/info populates last_active_at; carry it through only when
	// the field was actually present in the response. The pointer indirection
	// keeps "absent" distinct from "set but empty".
	if raw.LastActiveAt != nil {
		out.LastActiveAt = raw.LastActiveAt
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
