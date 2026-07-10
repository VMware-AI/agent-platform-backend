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
