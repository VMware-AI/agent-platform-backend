package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent/auditlog"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
)

func strptr(s string) *string { return &s }

// TestAuditLogs_FiltersAndActorName covers the T6 additions: result /
// resourceType / from-to filtering and batch actor-name resolution.
func TestAuditLogs_FiltersAndActorName(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	actor, err := r.Ent.User.Create().
		SetUsername("alice").SetEmail("alice@x.io").SetPasswordHash("x").SetRole("admin").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(action, resType, result string, actorID *uuid.UUID) {
		c := r.Ent.AuditLog.Create().SetAction(action).SetResourceType(resType).SetResult(auditlog.Result(result))
		if actorID != nil {
			c = c.SetActorUserID(*actorID)
		}
		if _, err := c.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mk("user.login", "user", "success", &actor.ID)
	mk("user.login", "user", "fail", &actor.ID)
	mk("gateway_connection.sync", "gateway_connection", "success", nil)

	// result filter
	qr := &queryResolver{r}
	res, err := qr.AuditLogs(adminCtx(), &model.AuditFilter{Result: strptr("fail")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Items[0].Result != "fail" {
		t.Errorf("result filter: want 1 fail, got total=%d", res.Total)
	}

	// resourceType filter
	res, _ = qr.AuditLogs(adminCtx(), &model.AuditFilter{ResourceType: strptr("gateway_connection")}, nil)
	if res.Total != 1 || res.Items[0].ActorName != nil {
		t.Errorf("resourceType filter: want 1 actorless row, got total=%d", res.Total)
	}

	// actor name resolved for rows with an actor
	res, _ = qr.AuditLogs(adminCtx(), &model.AuditFilter{ResourceType: strptr("user")}, nil)
	if res.Total != 2 {
		t.Fatalf("want 2 user rows, got %d", res.Total)
	}
	for _, it := range res.Items {
		if it.ActorName == nil || *it.ActorName != "alice" {
			t.Errorf("actor name not resolved: %+v", it.ActorName)
		}
	}

	// from/to window: nothing in the far future
	future := time.Now().Add(48 * time.Hour)
	res, _ = qr.AuditLogs(adminCtx(), &model.AuditFilter{From: &future}, nil)
	if res.Total != 0 {
		t.Errorf("from filter should exclude past rows, got %d", res.Total)
	}
}
