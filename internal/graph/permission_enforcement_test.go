package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// TestHasPermission_CustomRoleEnforcement proves the @hasPermission directive
// honors admin-configured custom roles (not just the static enum) — and that
// granting/revoking a role takes effect through the permission cache (C2).
func TestHasPermission_CustomRoleEnforcement(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	r.EnablePermissionCache(time.Hour) // long TTL: only invalidation can clear it
	ctx := adminCtx()
	mr := &mutationResolver{r}

	u, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "analyst", Email: "a@x.io", Password: "AnalystPass1", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	next := func(context.Context) (any, error) { return "ok", nil }
	userCtx := auth.WithCurrentUser(context.Background(), &auth.CurrentUser{ID: u.ID, Role: auth.RoleUser})

	// Plain user's static role lacks audit:view.
	if _, err := r.HasPermission(userCtx, nil, next, "audit:view"); err == nil {
		t.Fatal("plain user must be denied audit:view before any custom role")
	}

	// Grant audit:view via a custom role.
	role, err := mr.CreateCustomRole(ctx, model.CreateCustomRoleInput{Name: "auditor"})
	if err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	if _, err := mr.SetRolePermissions(ctx, role.ID, []string{"audit:view"}); err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}
	if _, err := mr.AssignUserRole(ctx, u.ID, role.ID); err != nil {
		t.Fatalf("AssignUserRole: %v", err)
	}

	// Now the custom role grants audit:view (cache was invalidated on assign).
	if _, err := r.HasPermission(userCtx, nil, next, "audit:view"); err != nil {
		t.Fatalf("custom role should grant audit:view: %v", err)
	}
	// But not a permission the role doesn't carry.
	if _, err := r.HasPermission(userCtx, nil, next, "key:manage"); err == nil {
		t.Fatal("custom role must not grant key:manage")
	}

	// Removing the role revokes immediately (not after TTL).
	if _, err := mr.RemoveUserRole(ctx, u.ID, role.ID); err != nil {
		t.Fatalf("RemoveUserRole: %v", err)
	}
	if _, err := r.HasPermission(userCtx, nil, next, "audit:view"); err == nil {
		t.Fatal("removing the role must revoke audit:view immediately")
	}
}
