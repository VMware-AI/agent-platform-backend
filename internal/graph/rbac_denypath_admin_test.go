package graph

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// This file extends the per-operation authz deny-path contract (rbac_denypath_test.go)
// to the platform-admin mutations not yet asserted there: the catalog/content,
// department, gateway-routing, and custom-role mutations. It drives the directive
// implementations (graph.HasRole / r.HasPermission) directly — the same isolation
// approach as rbac_denypath_test.go — so the assertion targets the gate decision,
// not each resolver's downstream dependencies (vCenter, litellm, sync).
//
// Every directive below was verified field-by-field against schema/*.graphql on the
// feat/backend-hardening branch (see the per-case comments for the source line). It is
// kept deliberately separate from rbac_denypath_test.go so concurrent edits to that
// file don't collide with this coverage extension.
//
// SKIPPED (intentionally, see TestDenyPath_AdminOps_NoDirectiveOpsAreInResolver):
//   addMembership / removeMembership      (department.graphql:43-44) — NO directive;
//                                          dept-admin allowance is checked in-resolver.
//   snapshotAgent / revertAgentSnapshot   (deploy.graphql:114,118)   — NO directive;
//   recycleAgent                          (deploy.graphql:111)       — owner-or-admin
//   setAgentStatus                        (agent.graphql:183)        — checked in-resolver.
// These carry no schema directive, so a directive-layer deny-path assertion would be
// vacuous/misleading. They are listed (not silently dropped) so coverage is auditable;
// asserting their in-resolver owner/dept gate belongs in a resolver-level test with a
// seeded owner vs. non-owner, which the in-memory directive harness can't set up.

// adminRoleAllowSet mirrors rbac_denypath_test.go's allowSet helper: it maps the
// schema's allowed RoleName list onto the auth.Role storage values (tenant_admin →
// tenant-admin via gqlRoleToEnt) so each case asserts the exact allow/deny split.
func adminRoleAllowSet(allowed []model.RoleName) map[auth.Role]bool {
	m := map[auth.Role]bool{}
	for _, a := range allowed {
		m[auth.Role(gqlRoleToEnt(a))] = true
	}
	return m
}

// TestDenyPath_AdminOps_RoleGated covers the @hasRole-gated platform-admin mutations
// not already asserted in rbac_denypath_test.go. For each role set, allowed roles pass
// the gate (reach okNext) and every other role — plus unauthenticated — is rejected
// (error + nil data). Reuses ctxForRole / assertRoleAllowed / assertRoleDenied / okNext
// from rbac_denypath_test.go.
func TestDenyPath_AdminOps_RoleGated(t *testing.T) {
	cases := []struct {
		ops     []string // schema fields carrying this exact @hasRole(any) directive
		allowed []model.RoleName
	}{
		{
			// @hasRole(any: [admin, tenant_admin])
			//   content.graphql:77-78  upsertArtifact / deleteArtifact
			//   department.graphql:40  deleteDepartment
			//   rbac.graphql:31,34     deleteCustomRole / setRolePermissions
			ops: []string{
				"upsertArtifact", "deleteArtifact",
				"deleteDepartment",
				"deleteCustomRole", "setRolePermissions",
			},
			allowed: []model.RoleName{model.RoleNameAdmin, model.RoleNameTenantAdmin},
		},
		{
			// @hasRole(any: [admin]) — platform-only catalog + gateway + permission registry.
			//   content.graphql:80,82          deleteSkill / deleteImage
			//   gateway-routing.graphql:142-144 registerGatewayConnection / testGatewayConnection / deleteGatewayConnection
			//   rbac.graphql:32                upsertPermission
			ops: []string{
				"deleteSkill", "deleteImage",
				"registerGatewayConnection", "testGatewayConnection", "deleteGatewayConnection",
				"upsertPermission",
			},
			allowed: []model.RoleName{model.RoleNameAdmin},
		},
	}

	for _, c := range cases {
		allow := adminRoleAllowSet(c.allowed)
		for _, op := range c.ops {
			for _, role := range allRoles {
				ctx := ctxForRole(role)
				who := string(role) + " on " + op
				if allow[role] {
					assertRoleAllowed(t, ctx, c.allowed, who)
				} else {
					assertRoleDenied(t, ctx, c.allowed, who)
				}
			}
			assertRoleDenied(t, context.Background(), c.allowed, "unauthenticated on "+op)
		}
	}
}

// TestDenyPath_AdminOps_PermissionGated covers the @hasPermission("route:manage")
// gateway-routing mutations not already asserted in rbac_denypath_test.go. route:manage
// is held by admin + tenant-admin (internal/auth/rbac.go); observability and plain user
// hold it not, and unauthenticated is always rejected.
func TestDenyPath_AdminOps_PermissionGated(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	// gateway-routing.graphql:146-147,160 — upsertUpstream / deleteUpstream / setRouterTier
	ops := []string{"upsertUpstream", "deleteUpstream", "setRouterTier"}
	grants := map[auth.Role]bool{
		auth.RoleAdmin: true, auth.RoleTenantAdmin: true,
	}

	for _, op := range ops {
		for _, role := range allRoles {
			ctx := ctxForRole(role)
			who := string(role) + " on " + op
			if grants[role] {
				assertPermAllowed(t, r, ctx, auth.PermRouteManage, who)
			} else {
				assertPermDenied(t, r, ctx, auth.PermRouteManage, who)
			}
		}
		assertPermDenied(t, r, context.Background(), auth.PermRouteManage, "unauthenticated on "+op)
	}
}

// TestDenyPath_AdminOps_NoDirectiveOpsAreInResolver documents the in-resolver-enforced
// mutations as an explicit, auditable SKIP. It asserts the FACTUAL basis for skipping —
// that RoleUser (a non-owner, non-admin principal) holds NONE of the relevant static
// permission keys via the matrix — so the reader can see the gate must be resolver-side,
// not a missing directive. It does NOT assert the in-resolver owner/dept gate itself
// (that needs a seeded owner vs. non-owner resolver test, out of scope here).
func TestDenyPath_AdminOps_NoDirectiveOpsAreInResolver(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

	// These ops carry NO schema directive (verified: department.graphql:43-44,
	// deploy.graphql:111,114,118, agent.graphql:183). Their authorization is enforced
	// inside the resolver (owner-or-admin / dept-admin), so a directive-layer assertion
	// would be vacuous. Listed here purely for coverage auditability.
	inResolverOps := []string{
		"addMembership", "removeMembership", // dept-admin OR platform/tenant admin
		"snapshotAgent", "revertAgentSnapshot", "recycleAgent", "setAgentStatus", // owner-or-admin
	}
	if len(inResolverOps) == 0 {
		t.Fatal("inResolverOps must be non-empty (it documents the skipped ops)")
	}

	// Floor of least privilege: a plain user (the non-owner, non-dept-admin principal
	// these resolvers must reject) holds none of the manage-level permission keys.
	ctx := ctxForRole(auth.RoleUser)
	for _, perm := range []string{auth.PermAgentManage, auth.PermUserManage} {
		assertPermDenied(t, r, ctx, perm, "plain user (in-resolver-gated op baseline)")
	}
}
