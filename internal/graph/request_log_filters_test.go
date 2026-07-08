package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
)

func TestRequestLogs_StatusClassAndTotal(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	mk := func(status int, user uuid.UUID) {
		if _, err := r.Ent.RequestLog.Create().
			SetRequestID(uuid.NewString()).SetStatusCode(status).SetLatencyMs(10).
			SetInputTokens(1).SetOutputTokens(1).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mk(200, uid)
	mk(404, uid)
	mk(500, uuid.New())

	qr := &queryResolver{r}
	cls := model.RequestStatusClassServerError
	res, err := qr.RequestLogs(ctx, &model.RequestLogFilter{StatusClass: &cls}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Items[0].StatusCode != 500 {
		t.Errorf("SERVER_ERROR filter: want 1×500, got total=%d", res.Total)
	}

	uidStr := uid.String()
	res, _ = qr.RequestLogs(ctx, &model.RequestLogFilter{UserID: &uidStr}, nil)
	if res.Total != 2 {
		t.Errorf("userId filter: want 2, got %d", res.Total)
	}

	future := time.Now().Add(48 * time.Hour)
	res, _ = qr.RequestLogs(ctx, &model.RequestLogFilter{From: &future}, nil)
	if res.Total != 0 {
		t.Errorf("from filter should exclude past rows, got %d", res.Total)
	}
}
