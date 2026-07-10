package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A real litellm /global/spend/report (group_by=team) shape — the contract this
// client parses. When you point this at a real litellm, compare against its
// /openapi.json (LLD-15 §3.2 notes the endpoint drifts across versions).
const teamReportJSON = `[
  {
    "group_by_day": "2026-07-01",
    "teams": [
      {"team_id": "t-eng", "team_name": "Engineering", "total_spend": 1.5,
       "metadata": [
         {"model": "gpt-4", "api_key": "sk-a", "spend": 1.0, "prompt_tokens": 600, "completion_tokens": 200, "total_tokens": 800},
         {"model": "gpt-3.5", "api_key": "sk-b", "spend": 0.5, "prompt_tokens": 300, "completion_tokens": 100, "total_tokens": 400}
       ]}
    ]
  }
]`

func TestGlobalSpendReport_ParsesTeamMetadata(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.String()
		if req.Header.Get("Authorization") != "Bearer sk-master" {
			t.Errorf("missing/wrong master key: %q", req.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(teamReportJSON))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	days, err := c.GlobalSpendReport(context.Background(), "2026-07-01", "2026-07-02", "team")
	if err != nil {
		t.Fatalf("GlobalSpendReport: %v", err)
	}
	if gotPath != "/global/spend/report?end_date=2026-07-02&group_by=team&start_date=2026-07-01" {
		t.Errorf("unexpected query: %s", gotPath)
	}
	if len(days) != 1 || len(days[0].Teams) != 1 {
		t.Fatalf("want 1 day/1 team, got %d days", len(days))
	}
	team := days[0].Teams[0]
	if team.ID != "t-eng" || team.Spend != 1.5 || len(team.Models) != 2 {
		t.Fatalf("team parsed wrong: %+v", team)
	}
	if team.Models[0].Model != "gpt-4" || team.Models[0].TotalTokens != 800 || team.Models[0].APIKey != "sk-a" {
		t.Errorf("model[0] parsed wrong: %+v", team.Models[0])
	}
}

func TestBudgetInfo_ParsesTeam(t *testing.T) {
	max := 100.0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/team/info" || req.URL.Query().Get("team_id") != "t-eng" {
			t.Errorf("unexpected request: %s", req.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"team_id": "t-eng", "team_alias": "Engineering", "spend": 42.0, "max_budget": max,
		})
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-master")
	info, err := c.BudgetInfo(context.Background(), BudgetScopeTeam, "t-eng")
	if err != nil {
		t.Fatalf("BudgetInfo: %v", err)
	}
	if info.Spend != 42.0 || info.MaxBudget == nil || *info.MaxBudget != 100.0 || info.Label != "Engineering" {
		t.Errorf("budget parsed wrong: %+v", info)
	}
}

// Real litellm /health omits healthy_count/unhealthy_count and only returns the
// endpoint arrays (verified against a live proxy) — the client must derive the
// counts from the array lengths.
func TestHealth_DerivesCountsFromArrays(t *testing.T) {
	body := `{"healthy_endpoints":[{"model":"gpt-4","api_base":"http://up1"}],
	          "unhealthy_endpoints":[{"model":"gpt-3.5","api_base":"http://up2","error":"conn refused"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/health" {
			t.Errorf("unexpected path %s", req.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, _ := NewHTTPClient(srv.URL, "sk-1234")
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Reachable || h.HealthyCount != 1 || h.UnhealthyCount != 1 {
		t.Errorf("counts not derived: %+v", h)
	}
	if len(h.Healthy) != 1 || h.Healthy[0].Model != "gpt-4" || h.Healthy[0].APIBase != "http://up1" {
		t.Errorf("healthy endpoint parsed wrong: %+v", h.Healthy)
	}
	if h.Unhealthy[0].Model != "gpt-3.5" {
		t.Errorf("unhealthy endpoint parsed wrong: %+v", h.Unhealthy)
	}
}
