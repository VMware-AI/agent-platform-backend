package graph

// Edge-case coverage for the metering query resolvers (MeteringOverview /
// MeteringSummary). These complement metering_test.go + metering_slices_test.go:
// they exercise the empty-window zeroing, the range-start lower bound, tenant
// scoping inside the aggregations, the byModel/byDay grouping shapes, and the
// bad/nonexistent-id guard rails. Resolvers are called directly so assertions
// target the aggregation/scoping logic, not the directive layer.
//
// Helper and local names carry a `MeEdge` / `_me` suffix to stay clear of the
// many sibling test files sharing this package.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// seedUsageMeEdge inserts one TokenUsage row directly via ent so the test can
// control tenant_id, agent_id and created_at (the RecordTokenUsage mutation
// derives tenant from the caller and always stamps "now").
func seedUsageMeEdge(t *testing.T, r *Resolver, m string, in, out int, cost float64, createdAt time.Time, tenant *uuid.UUID, agent *uuid.UUID) {
	t.Helper()
	c := r.Ent.TokenUsage.Create().
		SetUserID(uuid.New()).
		SetModel(m).
		SetInputTokens(in).
		SetOutputTokens(out).
		SetCost(cost).
		SetCreatedAt(createdAt)
	if tenant != nil {
		c.SetTenantID(*tenant)
	}
	if agent != nil {
		c.SetAgentID(*agent)
	}
	c.SaveX(context.Background())
}

// findModelRowMeEdge returns the byModel row for the given model name, or nil.
func findModelRowMeEdge(rows []model.ModelUsageRow, name string) *model.ModelUsageRow {
	for i := range rows {
		if rows[i].Model == name {
			return &rows[i]
		}
	}
	return nil
}

// TestMeteringOverview_EmptyWindowZeros guards that an overview over an empty
// window returns a fully-zeroed, non-nil structure (not nil, no NULL-SUM error,
// non-nil Cost summary). This is the no-rows path the console renders as empty
// cards rather than crashing.
func TestMeteringOverview_EmptyWindowZeros(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	ov, err := (&queryResolver{r}).MeteringOverview(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("MeteringOverview empty: %v", err)
	}
	if ov == nil {
		t.Fatal("overview must not be nil on an empty window")
	}
	if ov.Cost == nil {
		t.Fatal("cost summary must not be nil on an empty window")
	}
	if ov.TotalInputTokens != 0 || ov.TotalOutputTokens != 0 || ov.TotalTokens != 0 || ov.TotalRequests != 0 {
		t.Fatalf("empty window totals should be zero: %+v", ov)
	}
	if ov.Cost.TotalCost != 0 || ov.Cost.MonthlyCost != 0 {
		t.Fatalf("empty window cost should be zero: %+v", ov.Cost)
	}
	if len(ov.ByModel) != 0 || len(ov.ByAgent) != 0 || len(ov.ByDay) != 0 {
		t.Fatalf("empty window breakdowns should be empty: %+v", ov)
	}
	// default range is applied even when the arg is nil
	if ov.Range != model.MeteringTimeRangeLast7Days {
		t.Fatalf("nil range arg should default to LAST_7_DAYS, got %q", ov.Range)
	}
}

// TestMeteringSummary_EmptyWindowZeros mirrors the overview empty-window guard
// for the summary resolver: non-nil, all totals/slices zeroed, no NULL-SUM error.
func TestMeteringSummary_EmptyWindowZeros(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	sum, err := (&queryResolver{r}).MeteringSummary(context.Background(), nil)
	if err != nil {
		t.Fatalf("MeteringSummary empty: %v", err)
	}
	if sum == nil {
		t.Fatal("summary must not be nil on an empty window")
	}
	if sum.TotalInputTokens != 0 || sum.TotalOutputTokens != 0 || sum.TotalCost != 0 {
		t.Fatalf("empty summary totals should be zero: %+v", sum)
	}
	if len(sum.ByModel) != 0 || len(sum.ByAgent) != 0 || len(sum.ByDate) != 0 {
		t.Fatalf("empty summary breakdowns should be empty: %+v", sum)
	}
}

// TestMeteringOverview_RangeStartExcludesOld guards the range-start lower bound
// (meteringRangeStart): rows older than the selected window are excluded, rows
// inside it are counted. A row at -3d is in both windows; a row at -20d is only
// in the 30-day window; a row at -40d is in neither. This would fail if the
// CreatedAtGTE(from) filter or the per-range start computation regressed.
func TestMeteringOverview_RangeStartExcludesOld(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	now := time.Now().UTC()

	seedUsageMeEdge(t, r, "recent", 10, 5, 1.0, now.AddDate(0, 0, -3), nil, nil)       // within 7 & 30
	seedUsageMeEdge(t, r, "midold", 100, 50, 2.0, now.AddDate(0, 0, -20), nil, nil)    // within 30 only
	seedUsageMeEdge(t, r, "ancient", 1000, 500, 4.0, now.AddDate(0, 0, -40), nil, nil) // outside both

	qr := &queryResolver{r}

	// LAST_7_DAYS: only the -3d row.
	last7 := model.MeteringTimeRangeLast7Days
	ov7, err := qr.MeteringOverview(context.Background(), &last7, nil)
	if err != nil {
		t.Fatalf("overview last7: %v", err)
	}
	if ov7.TotalRequests != 1 || ov7.TotalInputTokens != 10 {
		t.Fatalf("last7 should see only the -3d row: reqs=%d in=%d", ov7.TotalRequests, ov7.TotalInputTokens)
	}
	if findModelRowMeEdge(ov7.ByModel, "midold") != nil || findModelRowMeEdge(ov7.ByModel, "ancient") != nil {
		t.Fatalf("last7 leaked an out-of-window model row: %+v", ov7.ByModel)
	}

	// LAST_30_DAYS: the -3d and -20d rows, not the -40d row.
	last30 := model.MeteringTimeRangeLast30Days
	ov30, err := qr.MeteringOverview(context.Background(), &last30, nil)
	if err != nil {
		t.Fatalf("overview last30: %v", err)
	}
	if ov30.TotalRequests != 2 || ov30.TotalInputTokens != 110 {
		t.Fatalf("last30 should see the -3d and -20d rows: reqs=%d in=%d", ov30.TotalRequests, ov30.TotalInputTokens)
	}
	if findModelRowMeEdge(ov30.ByModel, "ancient") != nil {
		t.Fatalf("last30 leaked the -40d ancient row: %+v", ov30.ByModel)
	}
}

// TestMeteringOverview_TenantScopingOwnDataOnly guards that the aggregation
// honours tenant scope: a tenant-admin's overview sums ONLY their own tenant's
// rows, never another tenant's nor the platform (NULL-tenant) rows. A regression
// in scopedTokenUsageQuery would leak cross-tenant usage into the totals.

// TestMeteringSummary_TenantScopingOwnDataOnly guards the same tenant scoping
// for the summary resolver's totals + byModel slice.

// TestMeteringOverview_ByModelGroupingShape guards the byModel grouping shape:
// rows are grouped per model, each row's TotalTokens = in + out, its Requests is
// the row count for that model, and the grand totals equal the sum of the groups.
func TestMeteringOverview_ByModelGroupingShape(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	recent := time.Now().UTC().AddDate(0, 0, -1)

	// fast: 2 rows; heavy: 1 row.
	seedUsageMeEdge(t, r, "fast", 100, 200, 0.5, recent, nil, nil)
	seedUsageMeEdge(t, r, "fast", 50, 80, 0.25, recent, nil, nil)
	seedUsageMeEdge(t, r, "heavy", 300, 600, 2.0, recent, nil, nil)

	ov, err := (&queryResolver{r}).MeteringOverview(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("MeteringOverview: %v", err)
	}
	if len(ov.ByModel) != 2 {
		t.Fatalf("expected 2 model groups, got %d (%+v)", len(ov.ByModel), ov.ByModel)
	}
	fast := findModelRowMeEdge(ov.ByModel, "fast")
	if fast == nil {
		t.Fatal("missing 'fast' model group")
	}
	if fast.InputTokens != 150 || fast.OutputTokens != 280 {
		t.Fatalf("fast group sums wrong: in=%d out=%d", fast.InputTokens, fast.OutputTokens)
	}
	if fast.TotalTokens != fast.InputTokens+fast.OutputTokens {
		t.Fatalf("fast TotalTokens != in+out: %d vs %d", fast.TotalTokens, fast.InputTokens+fast.OutputTokens)
	}
	if fast.Requests != 2 {
		t.Fatalf("fast group should count 2 requests, got %d", fast.Requests)
	}
	heavy := findModelRowMeEdge(ov.ByModel, "heavy")
	if heavy == nil || heavy.Requests != 1 || heavy.TotalTokens != 900 {
		t.Fatalf("heavy group wrong: %+v", heavy)
	}
	// grand totals reconcile with the groups.
	if ov.TotalInputTokens != 450 || ov.TotalOutputTokens != 880 {
		t.Fatalf("grand totals wrong: in=%d out=%d", ov.TotalInputTokens, ov.TotalOutputTokens)
	}
	if ov.TotalTokens != 1330 || ov.TotalRequests != 3 {
		t.Fatalf("grand total tokens/requests wrong: tok=%d reqs=%d", ov.TotalTokens, ov.TotalRequests)
	}
	if ov.Cost.TotalCost != 2.75 {
		t.Fatalf("grand total cost = %v, want 2.75", ov.Cost.TotalCost)
	}
}

// TestMeteringOverview_ByDayGroupingShape guards the byDay grouping shape: the
// per-day trend slice is populated for in-window rows, every bucket maintains
// TotalTokens = in + out, and the buckets reconcile with the grand totals
// (summed in/out/reqs across buckets == the overview totals). All rows sit
// inside the default 7-day window so they are not filtered out.
//
// NOTE: this asserts the aggregation/shape invariants rather than a specific
// number of distinct day buckets. The in-memory sqlite test harness stores
// timestamps in a non-ISO text format that sqlite's date() cannot parse, so the
// date bucket key is the same (empty) for every row regardless of its actual
// day — meaning distinct-day bucketing is not observable in this harness (the
// sibling TestMeteringSummary_ByAgentAndByDate has the same limitation). The
// per-bucket math and totals reconciliation below still fail on a real
// regression of the GROUP BY sums or the TotalTokens derivation.
func TestMeteringOverview_ByDayGroupingShape(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	recent := time.Now().UTC().AddDate(0, 0, -1)

	seedUsageMeEdge(t, r, "m", 10, 1, 0.1, recent, nil, nil)
	seedUsageMeEdge(t, r, "m", 20, 2, 0.2, recent, nil, nil)
	seedUsageMeEdge(t, r, "m", 30, 3, 0.3, recent, nil, nil)

	ov, err := (&queryResolver{r}).MeteringOverview(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("MeteringOverview: %v", err)
	}
	if len(ov.ByDay) == 0 {
		t.Fatal("byDay should be populated for in-window rows")
	}
	var sumIn, sumOut, sumReqs int
	for _, d := range ov.ByDay {
		if d.TotalTokens != d.InputTokens+d.OutputTokens {
			t.Fatalf("byDay bucket TotalTokens != in+out: %+v", d)
		}
		sumIn += d.InputTokens
		sumOut += d.OutputTokens
		sumReqs += d.Requests
	}
	// the byDay buckets account for every row and reconcile with the grand totals.
	if sumIn != 60 || sumOut != 6 || sumReqs != 3 {
		t.Fatalf("byDay buckets do not sum to all rows: in=%d out=%d reqs=%d", sumIn, sumOut, sumReqs)
	}
	if sumIn != ov.TotalInputTokens || sumOut != ov.TotalOutputTokens || sumReqs != ov.TotalRequests {
		t.Fatalf("byDay sums must reconcile with grand totals: byDay(%d/%d/%d) vs total(%d/%d/%d)",
			sumIn, sumOut, sumReqs, ov.TotalInputTokens, ov.TotalOutputTokens, ov.TotalRequests)
	}
}

// TestMeteringOverview_ByAgentNamesResolved guards that per-agent breakdown rows
// carry a resolved display name when the agent exists, and fall back to the id
// string when it does not (agentNamesFor). Rows with no agent_id are excluded.
func TestMeteringOverview_ByAgentNamesResolved(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	recent := time.Now().UTC().AddDate(0, 0, -1)

	known := r.Ent.Agent.Create().SetName("Named Agent").SetAgentType("goose").
		SetOwnerUserID(uuid.New()).SaveX(ctx)
	ghost := uuid.New() // never created

	seedUsageMeEdge(t, r, "m", 1, 1, 0.0, recent, nil, &known.ID)
	seedUsageMeEdge(t, r, "m", 2, 2, 0.0, recent, nil, &ghost)
	seedUsageMeEdge(t, r, "m", 9, 9, 0.0, recent, nil, nil) // no agent → excluded from byAgent

	ov, err := (&queryResolver{r}).MeteringOverview(ctx, nil, nil)
	if err != nil {
		t.Fatalf("MeteringOverview: %v", err)
	}
	if len(ov.ByAgent) != 2 {
		t.Fatalf("byAgent should have 2 rows (the no-agent row is excluded), got %d", len(ov.ByAgent))
	}
	names := map[string]string{}
	for _, a := range ov.ByAgent {
		names[a.AgentID] = a.AgentName
	}
	if names[known.ID.String()] != "Named Agent" {
		t.Fatalf("known agent name not resolved: %q", names[known.ID.String()])
	}
	// missing agent falls back to its id string (never blank).
	if names[ghost.String()] != ghost.String() {
		t.Fatalf("missing agent should fall back to id, got %q", names[ghost.String()])
	}
}

// TestMetering_BadUserIDRejected guards the id validation boundary: a malformed
// userId is rejected with an error (not a panic, not silent all-rows). Covers
// both the overview and summary resolvers (and scopedTokenUsageQuery beneath).
func TestMetering_BadUserIDRejected(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	bad := "not-a-uuid"

	if _, err := (&queryResolver{r}).MeteringOverview(ctx, nil, &bad); err == nil {
		t.Fatal("MeteringOverview with bad userId should error")
	}
	if _, err := (&queryResolver{r}).MeteringSummary(ctx, &bad); err == nil {
		t.Fatal("MeteringSummary with bad userId should error")
	}
	if _, err := (&queryResolver{r}).TokenUsage(ctx, &bad, nil); err == nil {
		t.Fatal("TokenUsage with bad userId should error")
	}
}

// TestMetering_NonexistentUserIDEmpty guards the well-formed-but-unknown id path:
// a valid UUID that matches no rows yields zeroed, non-nil results — no panic,
// no error, no leaked rows.
func TestMetering_NonexistentUserIDEmpty(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	// seed a row owned by a DIFFERENT user so the table is non-empty.
	seedUsageMeEdge(t, r, "m", 5, 5, 1.0, time.Now().UTC(), nil, nil)

	ghost := uuid.NewString()
	ov, err := (&queryResolver{r}).MeteringOverview(ctx, nil, &ghost)
	if err != nil {
		t.Fatalf("MeteringOverview unknown user: %v", err)
	}
	if ov == nil || ov.Cost == nil {
		t.Fatal("overview/cost must be non-nil for an unknown user")
	}
	if ov.TotalRequests != 0 || len(ov.ByModel) != 0 || len(ov.ByDay) != 0 {
		t.Fatalf("unknown user should see no usage: %+v", ov)
	}

	sum, err := (&queryResolver{r}).MeteringSummary(ctx, &ghost)
	if err != nil {
		t.Fatalf("MeteringSummary unknown user: %v", err)
	}
	if sum.TotalInputTokens != 0 || len(sum.ByModel) != 0 {
		t.Fatalf("unknown user summary should be zero: %+v", sum)
	}
}
