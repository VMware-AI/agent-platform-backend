package auth

import (
	"context"
	"testing"
)

func TestRolePermissionMatrix(t *testing.T) {
	cases := []struct {
		role Role
		perm string
		want bool
	}{
		// admin: all 6 permissions
		{RoleAdmin, PermUserManage, true},
		{RoleAdmin, PermAuditView, true},
		{RoleAdmin, PermMeteringView, true},
		{RoleAdmin, PermAgentManage, true},
		{RoleAdmin, PermKeyManage, true},
		{RoleAdmin, PermRouteManage, true},
		// read_only: NO permissions in the matrix. Read access flows through
		// @hasRole(any: [admin, read_only]) gates on the read-only fields,
		// NOT through this matrix (so writes stay admin-only by construction).
		// read_only is the observability seat (LLD-15 T7): audit + metering view.
		{RoleReadOnly, PermAuditView, true},
		{RoleReadOnly, PermMeteringView, true},
		{RoleReadOnly, PermUserManage, false},
		{RoleReadOnly, PermKeyManage, false},
		{RoleReadOnly, PermAgentManage, false},
		{RoleReadOnly, PermRouteManage, false},
		// user: same — owner-scoped reads/writes are enforced at the resolver.
		{RoleUser, PermAuditView, false},
		{RoleUser, PermMeteringView, false},
		{RoleUser, PermUserManage, false},
		{RoleUser, PermKeyManage, false},
		{RoleUser, PermAgentManage, false},
		{RoleUser, PermRouteManage, false},
	}
	for _, c := range cases {
		if got := c.role.HasPermission(c.perm); got != c.want {
			t.Errorf("%s.HasPermission(%q) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestCurrentUserContext(t *testing.T) {
	ctx := context.Background()
	if FromContext(ctx) != nil {
		t.Fatal("empty context should yield nil user")
	}
	u := &CurrentUser{ID: "u1", Role: RoleAdmin}
	ctx = WithCurrentUser(ctx, u)
	got := FromContext(ctx)
	if got == nil || got.ID != "u1" || got.Role != RoleAdmin {
		t.Fatalf("FromContext = %+v, want id=u1 role=admin", got)
	}
}
