package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

func TestCustomRolePermissions(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	role, err := mr.CreateCustomRole(ctx, model.CreateCustomRoleInput{Name: "observability-specialist"})
	if err != nil {
		t.Fatalf("CreateCustomRole: %v", err)
	}
	if len(role.Permissions) != 0 {
		t.Fatalf("new role should have no permissions")
	}

	// set permissions (auto-creates missing ones)
	updated, err := mr.SetRolePermissions(ctx, role.ID, []string{"audit:view", "metering:view"})
	if err != nil {
		t.Fatalf("SetRolePermissions: %v", err)
	}
	if len(updated.Permissions) != 2 {
		t.Fatalf("role should have 2 permissions, got %d", len(updated.Permissions))
	}
	if perms, _ := qr.Permissions(ctx); len(perms) < 2 {
		t.Fatalf("permissions catalog should have >=2, got %d", len(perms))
	}

	// re-set to a single permission (replace, not append)
	one, _ := mr.SetRolePermissions(ctx, role.ID, []string{"audit:view"})
	if len(one.Permissions) != 1 {
		t.Fatalf("permissions should be replaced to 1, got %d", len(one.Permissions))
	}

	// assign role to a user
	u := mkUser(t, mr, ctx, "obs", "o@x.io", model.RoleNameReadOnly)
	if ok, err := mr.AssignUserRole(ctx, u.ID, role.ID); err != nil || !ok {
		t.Fatalf("AssignUserRole: %v", err)
	}
	urs, _ := qr.UserRoles(ctx, u.ID)
	if len(urs) != 1 || urs[0].Name != "observability-specialist" {
		t.Fatalf("user roles wrong: %+v", urs)
	}

	if ok, err := mr.RemoveUserRole(ctx, u.ID, role.ID); err != nil || !ok {
		t.Fatalf("RemoveUserRole: %v", err)
	}
	if urs2, _ := qr.UserRoles(ctx, u.ID); len(urs2) != 0 {
		t.Fatalf("role not removed: %d", len(urs2))
	}

	if roles, _ := qr.CustomRoles(ctx); len(roles) != 1 {
		t.Fatalf("custom roles = %d", len(roles))
	}
}
