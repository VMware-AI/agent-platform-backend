package graph_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
)

// Verifies @hasPermission and @hasRole directives enforce through the real GraphQL
// executor (resolvers are also unit-tested directly, which bypasses directives).
//
// Note on roles: read_only is the observability seat — it holds audit:view +
// metering:view in rolePermissions (LLD-15 T7), so it passes @hasPermission on
// the observability fields, and also passes @hasRole(any: [admin, read_only])
// gates. It holds NO write perms, so mutations stay admin-only by construction.
func TestE2E_PermissionDirectives(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	userCookie := e.seedUser(t, "plain", user.RoleUser)
	readOnlyCookie := e.seedUser(t, "ro", user.RoleReadOnly)

	// --- @hasPermission-gated reads (audit:view) ---
	// read_only is the observability seat (LLD-15 T7): it HOLDS audit:view, so
	// @hasPermission(audit:view) passes for it. Plain user holds no perm → denied.
	const auditQ = `{ auditLogs { total } }`
	var aResp struct {
		AuditLogs struct{ Total int }
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(userCookie)); err == nil {
		t.Fatal("plain user must be denied audit:view")
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(readOnlyCookie)); err != nil {
		t.Fatalf("read_only must be allowed audit:view (observability seat, LLD-15 T7): %v", err)
	}
	// unauthenticated denied
	if err := e.gql.Post(auditQ, &aResp); err == nil {
		t.Fatal("unauthenticated must be denied audit:view")
	}
}

// TestE2E_ReadOnlyRoleGate verifies the schema @hasRole(any: [admin, read_only])
// gates that replaced the old observability perm-based access.
func TestE2E_ReadOnlyRoleGate(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	userCookie := e.seedUser(t, "plain", user.RoleUser)
	readOnlyCookie := e.seedUser(t, "ro", user.RoleReadOnly)
	adminCookie := e.seedUser(t, "boss", user.RoleAdmin)

	// virtualKeys list is gated @hasRole(any: [admin, read_only]) — read_only
	// passes, plain user is denied.
	const vkQ = `{ virtualKeys { id } }`
	var vResp struct {
		VirtualKeys []struct{ ID string }
	}
	if err := e.gql.Post(vkQ, &vResp, client.AddCookie(readOnlyCookie)); err != nil {
		t.Fatalf("read_only should pass virtualKeys: %v", err)
	}
	if err := e.gql.Post(vkQ, &vResp, client.AddCookie(userCookie)); err == nil {
		t.Fatal("plain user must be denied virtualKeys (admin+read_only only)")
	}
	// admin still works
	if err := e.gql.Post(vkQ, &vResp, client.AddCookie(adminCookie)); err != nil {
		t.Fatalf("admin should pass virtualKeys: %v", err)
	}
}
