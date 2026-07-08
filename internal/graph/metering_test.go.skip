package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestTokenUsage_StablePagination guards H1: paging through a list must
// partition the rows exactly — no row on two pages, none dropped. This requires
// a stable TOTAL order (created_at desc, id desc); created_at alone is not unique
// for these rapid inserts. (On Postgres an unordered query can reorder between
// pages; this guards against regressing the ORDER BY.)
func TestTokenUsage_StablePagination(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	uid := "11111111-1111-1111-1111-111111111111"
	const n = 25
	for i := 0; i < n; i++ {
		if _, err := mr.RecordTokenUsage(ctx, model.RecordTokenUsageInput{
			UserID: uid, Model: "smart", InputTokens: i + 1, OutputTokens: i + 1,
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	limit := 10
	for offset := 0; offset < n+limit; offset += limit {
		l, o := limit, offset
		rows, err := qr.TokenUsage(ctx, &uid, &model.PageInput{Limit: &l, Offset: &o})
		if err != nil {
			t.Fatalf("page offset %d: %v", offset, err)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatalf("row %s appeared on two pages — unstable pagination", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != n {
		t.Fatalf("paged %d distinct rows, want %d", len(seen), n)
	}
}

// TestMeteringSummary_Empty guards the no-rows path: pushed-down GROUP BY over
// an empty table returns zero groups (not a NULL SUM), so the summary is zeroed.
func TestMeteringSummary_Empty(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	sum, err := (&queryResolver{r}).MeteringSummary(context.Background(), nil)
	if err != nil {
		t.Fatalf("MeteringSummary empty: %v", err)
	}
	if sum.TotalInputTokens != 0 || sum.TotalCost != 0 || len(sum.ByModel) != 0 || len(sum.ByDate) != 0 {
		t.Fatalf("empty summary should be zero: %+v", sum)
	}
}

func TestRecordAndAggregateTokenUsage(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	uid := "11111111-1111-1111-1111-111111111111"
	cost := 0.5
	records := []model.RecordTokenUsageInput{
		{UserID: uid, Model: "tier-fast", InputTokens: 100, OutputTokens: 200, Cost: &cost},
		{UserID: uid, Model: "tier-fast", InputTokens: 50, OutputTokens: 80, Cost: &cost},
		{UserID: uid, Model: "tier-heavy", InputTokens: 300, OutputTokens: 600, Cost: &cost},
	}
	for _, rec := range records {
		if _, err := mr.RecordTokenUsage(ctx, rec); err != nil {
			t.Fatalf("RecordTokenUsage: %v", err)
		}
	}

	rows, err := qr.TokenUsage(ctx, &uid, nil)
	if err != nil {
		t.Fatalf("TokenUsage: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	sum, err := qr.MeteringSummary(ctx, &uid)
	if err != nil {
		t.Fatalf("MeteringSummary: %v", err)
	}
	if sum.TotalInputTokens != 450 || sum.TotalOutputTokens != 880 {
		t.Fatalf("totals wrong: in=%d out=%d", sum.TotalInputTokens, sum.TotalOutputTokens)
	}
	if sum.TotalCost != 1.5 {
		t.Fatalf("total cost = %v, want 1.5", sum.TotalCost)
	}
	if len(sum.ByModel) != 2 {
		t.Fatalf("expected 2 models, got %d", len(sum.ByModel))
	}
	// find tier-fast aggregate
	var fast *model.ModelUsage
	for i := range sum.ByModel {
		if sum.ByModel[i].Model == "tier-fast" {
			fast = &sum.ByModel[i]
		}
	}
	if fast == nil || fast.InputTokens != 150 || fast.OutputTokens != 280 {
		t.Fatalf("tier-fast aggregate wrong: %+v", fast)
	}
}
