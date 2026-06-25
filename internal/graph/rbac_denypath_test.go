package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// This file locks in the per-operation authz contract on the deny path: for every
// sensitive Query/Mutation field, a caller WITHOUT the required permission/role is
// rejected (error + nil data), and an authorized caller passes through. It drives
// the directive implementations (r.HasPermission / HasRole) directly — the same
// approach as integration_test.go (TestHasRoleDirective / TestHasPermissionDirective)
// and permission_enforcement_test.go — so the assertion targets the gate decision
// itself, isolated from each resolver's downstream dependencies (vCenter, litellm).
//
// The op→directive mapping below is kept in lock-step with the schema directives
// (verified field-by-field against schema/*.graphql). TestDirectiveCoverage already
// proves every root field IS gated; this test proves each gate gates the RIGHT
// principals. The static role→permission matrix lives in internal/auth/rbac.go.

// okNext is the resolver continuation: reaching it means the directive ALLOWED the
// call. A denied call must never reach it (data stays nil).
func okNext(context.Context) (any, error) { return "ok", nil }

// allRoles is the full set of platform roles, so each case can assert the exact
// allow/deny split across every principal (no role silently untested).
var allRoles = []auth.Role{auth.RoleAdmin, auth.RoleTenantAdmin, auth.RoleObservability, auth.RoleUser}

func ctxForRole(role auth.Role) context.Context {
	return auth.WithCurrentUser(context.Background(), &auth.CurrentUser{
		ID:       "00000000-0000-0000-0000-000000000009",
		Role:     role,
		TenantID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	})
}

// assertPermDenied: the @hasPermission gate must reject (error returned, data nil).
func assertPermDenied(t *testing.T, r *Resolver, ctx context.Context, perm, who string) {
	t.Helper()
	res, err := r.HasPermission(ctx, nil, okNext, perm)
	if err == nil {
		t.Errorf("%s must be DENIED %q (got no error)", who, perm)
	}
	if res != nil {
		t.Errorf("%s denied %q must yield nil data, got %v", who, perm, res)
	}
}

// assertPermAllowed: the @hasPermission gate must pass through to the resolver.
func assertPermAllowed(t *testing.T, r *Resolver, ctx context.Context, perm, who string) {
	t.Helper()
	res, err := r.HasPermission(ctx, nil, okNext, perm)
	if err != nil {
		t.Errorf("%s must be ALLOWED %q: %v", who, perm, err)
	}
	if res != "ok" {
		t.Errorf("%s allowed %q must reach the resolver (res=%v)", who, perm, res)
	}
}

func assertRoleDenied(t *testing.T, ctx context.Context, allowed []model.RoleName, who string) {
	t.Helper()
	res, err := HasRole(ctx, nil, okNext, allowed)
	if err == nil {
		t.Errorf("%s must be DENIED role-gate %v (got no error)", who, allowed)
	}
	if res != nil {
		t.Errorf("%s denied role-gate %v must yield nil data, got %v", who, allowed, res)
	}
}

func assertRoleAllowed(t *testing.T, ctx context.Context, allowed []model.RoleName, who string) {
	t.Helper()
	res, err := HasRole(ctx, nil, okNext, allowed)
	if err != nil {
		t.Errorf("%s must be ALLOWED role-gate %v: %v", who, allowed, err)
	}
	if res != "ok" {
		t.Errorf("%s allowed role-gate %v must reach the resolver (res=%v)", who, allowed, res)
	}
}

// permOps lists every @hasPermission-gated sensitive field with its exact perm key
// and the static-matrix roles that hold it (internal/auth/rbac.go). The test asserts
// holders pass and every other role (plus unauthenticated) is rejected.
func TestDenyPath_PermissionGatedOps(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	cases := []struct {
		ops    []string // schema fields carrying this exact @hasPermission(perm)
		perm   string
		grants map[auth.Role]bool
	}{
		{
			// audit:view → admin, observability, tenant-admin (rbac.go)
			ops:  []string{"requestLogs", "auditLogs", "requestMetrics"},
			perm: auth.PermAuditView,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleObservability: true, auth.RoleTenantAdmin: true,
			},
		},
		{
			// metering:view → admin, observability, tenant-admin (rbac.go)
			ops:  []string{"meteringOverview(perm-gate via field)"},
			perm: auth.PermMeteringView,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleObservability: true, auth.RoleTenantAdmin: true,
			},
		},
		{
			// key:manage → admin, tenant-admin (rbac.go)
			ops:  []string{"issueVirtualKey", "upsertRateLimitPolicy(key:manage variant)"},
			perm: auth.PermKeyManage,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleTenantAdmin: true,
			},
		},
		{
			// route:manage → admin, tenant-admin (rbac.go). This is the directive the
			// rate-limit-policy mutations actually carry in schema (route.manage).
			ops:  []string{"upsertRateLimitPolicy", "setRateLimitPolicyEnabled", "deleteRateLimitPolicy"},
			perm: auth.PermRouteManage,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleTenantAdmin: true,
			},
		},
		{
			// user:manage → admin, tenant-admin (rbac.go)
			ops:  []string{"createUser(perm variant)", "assignUserRole(perm variant)"},
			perm: auth.PermUserManage,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleTenantAdmin: true,
			},
		},
		{
			// agent:manage → admin, tenant-admin (rbac.go)
			ops:  []string{"agent:manage gated ops"},
			perm: auth.PermAgentManage,
			grants: map[auth.Role]bool{
				auth.RoleAdmin: true, auth.RoleTenantAdmin: true,
			},
		},
	}

	for _, c := range cases {
		for _, role := range allRoles {
			ctx := ctxForRole(role)
			who := string(role) + " on " + c.ops[0]
			if c.grants[role] {
				assertPermAllowed(t, r, ctx, c.perm, who)
			} else {
				assertPermDenied(t, r, ctx, c.perm, who)
			}
		}
		// unauthenticated is always rejected.
		assertPermDenied(t, r, context.Background(), c.perm, "unauthenticated on "+c.ops[0])
	}
}

// roleOps lists every @hasRole-gated sensitive field with its exact allowed-role set
// (verified against schema/*.graphql). The test asserts allowed roles pass and every
// other role (plus unauthenticated) is rejected — including the hyphen/underscore
// mapping (tenant_admin enum → tenant-admin storage, directives.go).
func TestDenyPath_RoleGatedOps(t *testing.T) {
	cases := []struct {
		ops     []string
		allowed []model.RoleName
	}{
		{
			// @hasRole(any: [admin, tenant_admin])
			ops:     []string{"createUser", "assignUserRole", "createCustomRole", "createDepartment", "createAgentConfig", "setAgentConfigKnowledge"},
			allowed: []model.RoleName{model.RoleNameAdmin, model.RoleNameTenantAdmin},
		},
		{
			// @hasRole(any: [admin]) — platform-only catalog + routing + deploy.
			ops:     []string{"upsertSkill", "upsertImage", "upsertAgentTemplate", "deployAgent", "modelRoutes", "createModelRoute", "updateModelRoute", "deleteModelRoute", "setModelRouteEnabled"},
			allowed: []model.RoleName{model.RoleNameAdmin},
		},
	}

	allowSet := func(allowed []model.RoleName) map[auth.Role]bool {
		m := map[auth.Role]bool{}
		for _, a := range allowed {
			m[auth.Role(gqlRoleToEnt(a))] = true
		}
		return m
	}

	for _, c := range cases {
		allow := allowSet(c.allowed)
		for _, role := range allRoles {
			ctx := ctxForRole(role)
			who := string(role) + " on " + c.ops[0]
			if allow[role] {
				assertRoleAllowed(t, ctx, c.allowed, who)
			} else {
				assertRoleDenied(t, ctx, c.allowed, who)
			}
		}
		assertRoleDenied(t, context.Background(), c.allowed, "unauthenticated on "+c.ops[0])
	}
}

// TestDenyPath_RoleUserHoldsNoStaticPermission asserts the floor of least privilege:
// RoleUser carries NONE of the platform/tenant permission keys via the static matrix
// (own-scoped access is resolved per-resource, not granted here — rbac.go §4.1).
func TestDenyPath_RoleUserHoldsNoStaticPermission(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := ctxForRole(auth.RoleUser)
	for _, perm := range []string{
		auth.PermAuditView, auth.PermMeteringView, auth.PermKeyManage,
		auth.PermRouteManage, auth.PermUserManage, auth.PermAgentManage,
	} {
		assertPermDenied(t, r, ctx, perm, "plain user")
	}
}
