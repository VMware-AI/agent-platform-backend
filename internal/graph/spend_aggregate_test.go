package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// fakeSpendReader returns scripted days (or an error) for one gateway.
type fakeSpendReader struct {
	days []gateway.SpendReportDay
	err  error
}

func (f fakeSpendReader) GlobalSpendReport(_ context.Context, _, _, _ string) ([]gateway.SpendReportDay, error) {
	return f.days, f.err
}

func (f fakeSpendReader) BudgetInfo(_ context.Context, _ gateway.BudgetInfoScope, _ string) (*gateway.BudgetInfo, error) {
	return nil, nil
}

func day(date string, teams ...gateway.TeamSpend) gateway.SpendReportDay {
	return gateway.SpendReportDay{Date: date, Teams: teams}
}

func teamWithModels(id, name string, models ...gateway.ModelSpend) gateway.TeamSpend {
	var spend float64
	for _, m := range models {
		spend += m.Spend
	}
	return gateway.TeamSpend{ID: id, Name: name, Spend: spend, Models: models}
}

func spendInput(g model.SpendGroupBy) model.SpendReportInput {
	return model.SpendReportInput{
		From:    time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		GroupBy: g,
	}
}

// twoGatewayResolver wires two gateways whose SpendReaders are keyed by name.
func twoGatewayResolver(t *testing.T, byName map[string]gateway.SpendReader) (*Resolver, func()) {
	t.Helper()
	r, cleanup := newTestResolver(t)
	ctx := context.Background()
	if _, err := r.Ent.GatewayConnection.Create().SetName("gw-a").SetEndpoint("http://a:4000").SetIsDefault(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ent.GatewayConnection.Create().SetName("gw-b").SetEndpoint("http://b:4000").Save(ctx); err != nil {
		t.Fatal(err)
	}
	r.SpendReaderFor = func(_ context.Context, g *ent.GatewayConnection) gateway.SpendReader {
		return byName[g.Name]
	}
	return r, cleanup
}

func TestSpendReport_MergesTeamsAcrossGateways(t *testing.T) {
	// Same team t-eng on both gateways → spend and tokens sum.
	a := fakeSpendReader{days: []gateway.SpendReportDay{
		day("2026-07-01", teamWithModels("t-eng", "Engineering",
			gateway.ModelSpend{Model: "gpt-4", APIKey: "sk-a", Spend: 1.0, TotalTokens: 800})),
	}}
	b := fakeSpendReader{days: []gateway.SpendReportDay{
		day("2026-07-01", teamWithModels("t-eng", "Engineering",
			gateway.ModelSpend{Model: "gpt-4", APIKey: "sk-b", Spend: 0.5, TotalTokens: 400})),
	}}
	r, cleanup := twoGatewayResolver(t, map[string]gateway.SpendReader{"gw-a": a, "gw-b": b})
	defer cleanup()

	rep, err := r.spendReport(context.Background(), spendInput(model.SpendGroupByTeam))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("want 1 merged team row, got %d", len(rep.Rows))
	}
	if rep.Rows[0].Spend != 1.5 || rep.Rows[0].TotalTokens != 1200 {
		t.Errorf("cross-gateway sum wrong: %+v", rep.Rows[0])
	}
	if rep.Totals.Spend != 1.5 {
		t.Errorf("totals wrong: %+v", rep.Totals)
	}
	if len(rep.Gateways) != 2 || !rep.Gateways[0].Ok || !rep.Gateways[1].Ok {
		t.Errorf("both gateways should be ok: %+v", rep.Gateways)
	}
}

func TestSpendReport_ModelDimensionReGroups(t *testing.T) {
	a := fakeSpendReader{days: []gateway.SpendReportDay{
		day("2026-07-01", teamWithModels("t-eng", "Engineering",
			gateway.ModelSpend{Model: "gpt-4", Spend: 1.0, TotalTokens: 800},
			gateway.ModelSpend{Model: "gpt-3.5", Spend: 0.5, TotalTokens: 400})),
	}}
	r, cleanup := twoGatewayResolver(t, map[string]gateway.SpendReader{
		"gw-a": a, "gw-b": fakeSpendReader{},
	})
	defer cleanup()

	rep, err := r.spendReport(context.Background(), spendInput(model.SpendGroupByModel))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("want 2 model rows, got %d", len(rep.Rows))
	}
	// Sorted by spend desc → gpt-4 first.
	if rep.Rows[0].Key != "gpt-4" || rep.Rows[0].Spend != 1.0 {
		t.Errorf("model rows not re-grouped/sorted: %+v", rep.Rows)
	}
}

func TestSpendReport_PartialGatewayFailureDegrades(t *testing.T) {
	a := fakeSpendReader{days: []gateway.SpendReportDay{
		day("2026-07-01", teamWithModels("t-eng", "Engineering",
			gateway.ModelSpend{Model: "gpt-4", Spend: 1.0, TotalTokens: 800})),
	}}
	bad := fakeSpendReader{err: context.DeadlineExceeded}
	r, cleanup := twoGatewayResolver(t, map[string]gateway.SpendReader{"gw-a": a, "gw-b": bad})
	defer cleanup()

	rep, err := r.spendReport(context.Background(), spendInput(model.SpendGroupByTeam))
	if err != nil {
		t.Fatalf("partial failure must not fail the whole report: %v", err)
	}
	if rep.Totals.Spend != 1.0 {
		t.Errorf("healthy gateway data should still show: %+v", rep.Totals)
	}
	failed := 0
	for _, g := range rep.Gateways {
		if !g.Ok {
			failed++
			if g.Error == nil {
				t.Error("failed gateway should carry an error string")
			}
		}
	}
	if failed != 1 {
		t.Errorf("want exactly 1 failed gateway, got %d", failed)
	}
}

func TestSpendReport_UserDimensionDeferred(t *testing.T) {
	r, cleanup := twoGatewayResolver(t, map[string]gateway.SpendReader{
		"gw-a": fakeSpendReader{}, "gw-b": fakeSpendReader{},
	})
	defer cleanup()
	if _, err := r.spendReport(context.Background(), spendInput(model.SpendGroupByUser)); err == nil {
		t.Error("USER dimension should return a clear not-yet error in the sample phase")
	}
}

func TestSpendCache_HitsWithinTTL(t *testing.T) {
	calls := 0
	counting := countingReader{onCall: func() { calls++ }}
	r, cleanup := twoGatewayResolver(t, map[string]gateway.SpendReader{"gw-a": counting, "gw-b": counting})
	defer cleanup()
	r.EnableSpendCache(time.Minute)

	in := spendInput(model.SpendGroupByTeam)
	if _, err := r.spendReport(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := r.spendReport(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if calls != 2 { // 2 gateways × 1 fan-out; second call served from cache
		t.Errorf("expected one fan-out (2 gateway calls), got %d", calls)
	}
}

type countingReader struct{ onCall func() }

func (c countingReader) GlobalSpendReport(_ context.Context, _, _, _ string) ([]gateway.SpendReportDay, error) {
	c.onCall()
	return nil, nil
}
func (c countingReader) BudgetInfo(_ context.Context, _ gateway.BudgetInfoScope, _ string) (*gateway.BudgetInfo, error) {
	return nil, nil
}
