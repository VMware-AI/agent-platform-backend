package graph

import (
	"strings"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestAuditLogFilters(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	// generate audit entries of different categories
	if _, err := mr.CreateUser(ctx, model.CreateUserInput{Username: "audita", DisplayName: "audita", Email: "a@x.io", RoleID: builtinRoleUUID(string(model.RoleNameUser)), PasswordMode: model.PasswordModeCustom, CustomPassword: ptr("AuditPass1234")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rpm := 60
	if _, err := mr.UpsertRateLimitPolicy(ctx, model.UpsertRateLimitPolicyInput{Name: "p", Rpm: &rpm}); err != nil {
		t.Fatalf("UpsertRateLimitPolicy: %v", err)
	}

	// filter by action category prefix
	prefix := "user."
	conn, err := qr.AuditLogs(ctx, &model.AuditFilter{ActionPrefix: &prefix}, nil)
	if err != nil {
		t.Fatalf("AuditLogs: %v", err)
	}
	if conn.Total == 0 {
		t.Fatal("expected user.* audit entries")
	}
	for _, it := range conn.Items {
		if !strings.HasPrefix(it.Action, "user.") {
			t.Fatalf("actionPrefix filter leaked: %s", it.Action)
		}
	}

	// substring search
	search := "rate_limit"
	conn2, err := qr.AuditLogs(ctx, &model.AuditFilter{Search: &search}, nil)
	if err != nil {
		t.Fatalf("AuditLogs search: %v", err)
	}
	if conn2.Total == 0 {
		t.Fatal("expected a rate_limit audit entry via search")
	}

	// filter by actor (adminCtx fixed id)
	actor := "00000000-0000-0000-0000-000000000001"
	conn3, _ := qr.AuditLogs(ctx, &model.AuditFilter{ActorUserID: &actor}, nil)
	if conn3.Total == 0 {
		t.Fatal("expected entries by the admin actor")
	}
}
