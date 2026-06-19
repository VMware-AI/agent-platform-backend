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
		{RoleAdmin, PermUserManage, true},
		{RoleAdmin, PermAuditView, true},
		{RoleObservability, PermAuditView, true},
		{RoleObservability, PermMeteringView, true},
		{RoleObservability, PermUserManage, false}, // observability cannot manage users
		{RoleObservability, PermKeyManage, false},
		{RoleUser, PermAuditView, false},
		{RoleUser, PermUserManage, false},
		{RoleTenantAdmin, PermUserManage, true},
	}
	for _, c := range cases {
		if got := c.role.HasPermission(c.perm); got != c.want {
			t.Errorf("%s.HasPermission(%q) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestHasAnyRole(t *testing.T) {
	if !RoleAdmin.HasAnyRole(RoleAdmin, RoleTenantAdmin) {
		t.Error("admin should match")
	}
	if RoleUser.HasAnyRole(RoleAdmin, RoleTenantAdmin) {
		t.Error("user should not match admin/tenant-admin")
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
