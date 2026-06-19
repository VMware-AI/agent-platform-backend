package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

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
