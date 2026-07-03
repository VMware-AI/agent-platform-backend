package graph

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/department"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// dateFmt is litellm's spend-report date format (YYYY-MM-DD).
const dateFmt = "2006-01-02"

// buildSpendReader returns a litellm spend/budget reader for one gateway. The
// injected SpendReaderFor wins (tests); otherwise a real HTTP client bound to
// the gateway's endpoint + master key (*HTTPClient satisfies SpendReader).
func (r *Resolver) buildSpendReader(ctx context.Context, g *ent.GatewayConnection) (gateway.SpendReader, error) {
	if r.SpendReaderFor != nil {
		return r.SpendReaderFor(ctx, g), nil
	}
	return gateway.NewHTTPClient(g.Endpoint, r.gatewayMasterKey(ctx, g))
}

// litellmGroupBy maps our dimension to the litellm group_by. TEAM/MODEL/API_KEY
// all pull the "team" report (its per-day teams[]→metadata[] carries the model
// and api_key breakdown, so one fetch re-groups into all three). USER needs
// group_by=internal_user_id and is deferred to the copy phase (LLD-15 T4+).
func litellmGroupBy(g model.SpendGroupBy) (string, error) {
	switch g {
	case model.SpendGroupByTeam, model.SpendGroupByModel, model.SpendGroupByAPIKey:
		return "team", nil
	case model.SpendGroupByUser:
		return "", gqlerror.Errorf("USER 维度将在复制阶段接入(group_by=internal_user_id)")
	default:
		return "", gqlerror.Errorf("invalid groupBy")
	}
}

// spendReport fans out to every configured gateway, merges the per-day team
// reports, and re-groups by the requested dimension. A single gateway failure
// degrades to a gateways[].ok=false entry rather than failing the whole report.
func (r *Resolver) spendReport(ctx context.Context, in model.SpendReportInput) (*model.SpendReport, error) {
	if in.To.Before(in.From) {
		return nil, gqlerror.Errorf("invalid range: to is before from")
	}
	if _, err := litellmGroupBy(in.GroupBy); err != nil {
		return nil, err
	}
	if cached := r.spendCache.get(in); cached != nil {
		return cached, nil
	}

	conns, err := r.Ent.GatewayConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	start, end := in.From.UTC().Format(dateFmt), in.To.UTC().Format(dateFmt)
	llmGroup, _ := litellmGroupBy(in.GroupBy)

	// Fan out concurrently; each goroutine owns its own slot, so no lock needed.
	perGateway := make([][]gateway.SpendReportDay, len(conns))
	statuses := make([]model.GatewaySpendStatus, len(conns))
	var wg sync.WaitGroup
	for i, g := range conns {
		wg.Add(1)
		go func(i int, g *ent.GatewayConnection) {
			defer wg.Done()
			st := model.GatewaySpendStatus{GatewayID: g.ID.String(), GatewayName: g.Name, Ok: true}
			reader, err := r.buildSpendReader(ctx, g)
			if err != nil {
				msg := err.Error()
				st.Ok, st.Error = false, &msg
				statuses[i] = st
				return
			}
			days, err := reader.GlobalSpendReport(ctx, start, end, llmGroup)
			if err != nil {
				msg := err.Error()
				st.Ok, st.Error = false, &msg
			}
			perGateway[i], statuses[i] = days, st
		}(i, g)
	}
	wg.Wait()

	report := mergeSpend(ctx, r, in, perGateway, statuses)
	r.spendCache.put(in, report)
	return report, nil
}

// aggRow accumulates one dimension key across gateways and days.
type aggRow struct {
	key, label                            string
	spend                                 float64
	promptTokens, completionTokens, total int
}

// mergeSpend re-groups the per-gateway day reports into rows for the requested
// dimension, a daily cost trend, and totals; resolves team labels to department
// names. Exported-ish via method for testability of the pure merge.
func mergeSpend(
	ctx context.Context,
	r *Resolver,
	in model.SpendReportInput,
	perGateway [][]gateway.SpendReportDay,
	statuses []model.GatewaySpendStatus,
) *model.SpendReport {
	rows := map[string]*aggRow{}
	byDay := map[string]*model.SpendDailyPoint{}
	teamLabels := r.resolveTeamLabels(ctx, perGateway)

	add := func(key, label string, spend float64, pt, ct, tt int) {
		row := rows[key]
		if row == nil {
			row = &aggRow{key: key, label: label}
			rows[key] = row
		}
		if row.label == "" {
			row.label = label
		}
		row.spend += spend
		row.promptTokens += pt
		row.completionTokens += ct
		row.total += tt
	}

	for _, days := range perGateway {
		for _, day := range days {
			dp := byDay[day.Date]
			if dp == nil {
				dp = &model.SpendDailyPoint{Date: day.Date}
				byDay[day.Date] = dp
			}
			for _, t := range day.Teams {
				dp.Spend += t.Spend
				for _, m := range t.Models {
					dp.TotalTokens += m.TotalTokens
					switch in.GroupBy {
					case model.SpendGroupByTeam:
						label := labelOr(teamLabels[t.ID], t.Name, t.ID)
						add(t.ID, label, m.Spend, m.PromptTokens, m.CompletionTokens, m.TotalTokens)
					case model.SpendGroupByModel:
						add(m.Model, m.Model, m.Spend, m.PromptTokens, m.CompletionTokens, m.TotalTokens)
					case model.SpendGroupByAPIKey:
						add(m.APIKey, m.APIKey, m.Spend, m.PromptTokens, m.CompletionTokens, m.TotalTokens)
					}
				}
			}
		}
	}

	return assembleReport(in, rows, byDay, statuses)
}

// resolveTeamLabels maps litellm team_id → department name for the ids present
// in the reports (one query, not per-row).
func (r *Resolver) resolveTeamLabels(ctx context.Context, perGateway [][]gateway.SpendReportDay) map[string]string {
	ids := map[string]struct{}{}
	for _, days := range perGateway {
		for _, day := range days {
			for _, t := range day.Teams {
				if t.ID != "" {
					ids[t.ID] = struct{}{}
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	teamIDs := make([]string, 0, len(ids))
	for id := range ids {
		teamIDs = append(teamIDs, id)
	}
	depts, err := r.Ent.Department.Query().
		Where(department.LitellmTeamIDIn(teamIDs...)).
		All(ctx)
	if err != nil {
		return nil
	}
	labels := make(map[string]string, len(depts))
	for _, d := range depts {
		labels[d.LitellmTeamID] = d.Name
	}
	return labels
}

func assembleReport(
	in model.SpendReportInput,
	rows map[string]*aggRow,
	byDay map[string]*model.SpendDailyPoint,
	statuses []model.GatewaySpendStatus,
) *model.SpendReport {
	out := make([]model.SpendRow, 0, len(rows))
	totals := model.SpendTotals{}
	for _, row := range rows {
		out = append(out, model.SpendRow{
			Key:              row.key,
			Label:            row.label,
			Spend:            row.spend,
			PromptTokens:     row.promptTokens,
			CompletionTokens: row.completionTokens,
			TotalTokens:      row.total,
		})
		totals.Spend += row.spend
		totals.PromptTokens += row.promptTokens
		totals.CompletionTokens += row.completionTokens
		totals.TotalTokens += row.total
	}
	// Highest spend first — the console leads with the top consumers.
	sort.Slice(out, func(i, j int) bool { return out[i].Spend > out[j].Spend })

	trend := make([]model.SpendDailyPoint, 0, len(byDay))
	for _, dp := range byDay {
		trend = append(trend, *dp)
	}
	sort.Slice(trend, func(i, j int) bool { return trend[i].Date < trend[j].Date })

	if statuses == nil {
		statuses = []model.GatewaySpendStatus{}
	}
	return &model.SpendReport{
		From:     in.From,
		To:       in.To,
		GroupBy:  in.GroupBy,
		Rows:     out,
		Totals:   &totals,
		ByDay:    trend,
		Gateways: statuses,
	}
}

// budgets lists budget cards for the requested scope. TEAMS is the sample-page
// scope (departments are few and each maps to a litellm team); USERS/KEYS are
// deferred to the copy phase (they need bounded enumeration) and return empty.
func (r *Resolver) budgets(ctx context.Context, scope model.BudgetScope) ([]model.Budget, error) {
	if scope != model.BudgetScopeTeams {
		return []model.Budget{}, nil
	}
	depts, err := r.Ent.Department.Query().
		Where(department.LitellmTeamIDNEQ("")).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Budget, 0, len(depts))
	for _, d := range depts {
		g, err := r.resolveDeptGateway(ctx, &d.ID)
		if err != nil || g == nil {
			continue
		}
		reader, err := r.buildSpendReader(ctx, g)
		if err != nil {
			continue
		}
		info, err := reader.BudgetInfo(ctx, gateway.BudgetScopeTeam, d.LitellmTeamID)
		if err != nil {
			continue
		}
		out = append(out, budgetCard(d.Name, info))
	}
	return out, nil
}

func budgetCard(label string, info *gateway.BudgetInfo) model.Budget {
	b := model.Budget{Scope: info.ID, Label: label, Spend: info.Spend, MaxBudget: info.MaxBudget}
	if info.MaxBudget != nil && *info.MaxBudget > 0 {
		remaining := *info.MaxBudget - info.Spend
		b.Remaining = &remaining
		pct := info.Spend / *info.MaxBudget * 100
		b.UtilizationPct = &pct
	}
	if info.BudgetResetAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.BudgetResetAt); err == nil {
			b.BudgetResetAt = &t
		}
	}
	return b
}

func labelOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- short-TTL cache (LLD-15 §3.6) ---

type spendReportCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]spendCacheEntry
}

type spendCacheEntry struct {
	report *model.SpendReport
	expiry time.Time
}

// EnableSpendCache turns on the short-lived spend-report cache (call from main).
func (r *Resolver) EnableSpendCache(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	r.spendCache = &spendReportCache{ttl: ttl, entries: map[string]spendCacheEntry{}}
}

func spendCacheKey(in model.SpendReportInput) string {
	return fmt.Sprintf("%d|%d|%s", in.From.Unix(), in.To.Unix(), in.GroupBy)
}

func (c *spendReportCache) get(in model.SpendReportInput) *model.SpendReport {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[spendCacheKey(in)]
	if !ok || time.Now().After(e.expiry) {
		return nil
	}
	return e.report
}

func (c *spendReportCache) put(in model.SpendReportInput, report *model.SpendReport) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[spendCacheKey(in)] = spendCacheEntry{report: report, expiry: time.Now().Add(c.ttl)}
}
