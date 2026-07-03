package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestRequestLogs(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	in := 100
	out := 200
	lat := 1234
	detail := `{"request":"x","response":null}`
	for _, sc := range []int{200, 200, 401} {
		if _, err := mr.RecordRequestLog(ctx, model.RecordRequestLogInput{
			RequestID: "req-x", InputTokens: &in, OutputTokens: &out, LatencyMs: &lat, StatusCode: sc, Detail: &detail,
		}); err != nil {
			t.Fatalf("RecordRequestLog: %v", err)
		}
	}

	all, err := qr.RequestLogs(ctx, nil, nil)
	if err != nil {
		t.Fatalf("RequestLogs: %v", err)
	}
	if len(all.Items) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(all.Items))
	}
	if all.Items[0].Detail == nil || *all.Items[0].Detail != detail {
		t.Fatalf("detail not stored/returned: %+v", all.Items[0].Detail)
	}

	code := 401
	errs, err := qr.RequestLogs(ctx, &model.RequestLogFilter{StatusCode: &code}, nil)
	if err != nil {
		t.Fatalf("RequestLogs filtered: %v", err)
	}
	if len(errs.Items) != 1 || errs.Items[0].StatusCode != 401 {
		t.Fatalf("status filter failed: %+v", errs)
	}

	// filter by requestId
	rid := "req-x"
	byReq, _ := qr.RequestLogs(ctx, &model.RequestLogFilter{RequestID: &rid}, nil)
	if len(byReq.Items) != 3 {
		t.Fatalf("requestId filter: got %d", len(byReq.Items))
	}
}

func TestRateLimitPolicy(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// Admin ctx: SetRateLimitPolicyEnabled/DeleteRateLimitPolicy carry a tenant 404
	// oracle (writeAllowed), so the success path needs an authed caller — in prod the
	// @hasRole directive guarantees one.
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	rpm := 60
	tpm := 200000
	enabled := true
	p, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{
		Name: "global_api_limit", Rpm: &rpm, Tpm: &tpm, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("UpsertRateLimitPolicy: %v", err)
	}
	if p.Rpm == nil || *p.Rpm != 60 || !p.Enabled {
		t.Fatalf("unexpected policy: %+v", p)
	}

	// upsert again (same name) updates, not duplicates
	newRpm := 120
	if _, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{
		Name: "global_api_limit", Rpm: &newRpm,
	}); err != nil {
		t.Fatalf("update policy: %v", err)
	}
	list, _ := qr.RateLimitPolicies(ctx)
	if len(list) != 1 {
		t.Fatalf("expected 1 policy after upsert, got %d", len(list))
	}
	if list[0].Rpm == nil || *list[0].Rpm != 120 || list[0].Enabled {
		t.Fatalf("policy not updated/disabled: %+v", list[0])
	}

	// toggle enabled
	toggled, err := mr.SetRateLimitPolicyEnabled(ctx, p.ID, true)
	if err != nil || !toggled.Enabled {
		t.Fatalf("SetRateLimitPolicyEnabled: %v", err)
	}
}
