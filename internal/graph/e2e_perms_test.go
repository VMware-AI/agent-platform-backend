package graph_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
)

// Verifies @hasPermission and @hasRole directives enforce through the real GraphQL
// executor (resolvers are also unit-tested directly, which bypasses directives).
//
// Note on the 3-role refactor: read_only has NO entries in rolePermissions; its
// read access is granted via explicit @hasRole(any: [admin, read_only]) gates on
// the read-only fields. So tests asserting "read_only passes via perm" no longer
// apply — read_only passes via role gate (or, on @hasPermission-only fields,
// fails because it has no perm).
func TestE2E_PermissionDirectives(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	userCookie := e.seedUser(t, "plain", user.RoleUser)
	readOnlyCookie := e.seedUser(t, "ro", user.RoleReadOnly)

	// --- @hasPermission-gated reads (audit:view) ---
	// After the refactor read_only has NO perm, so it gets denied at @hasPermission
	// just like user. Read-only audit access flows through role gates on the
	// schema instead (see TestE2E_ReadOnlyRoleGate below).
	const auditQ = `{ auditLogs { total } }`
	var aResp struct {
		AuditLogs struct{ Total int }
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(userCookie)); err == nil {
		t.Fatal("plain user must be denied audit:view")
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(readOnlyCookie)); err == nil {
		t.Fatal("read_only must be denied audit:view (perm matrix is empty for it)")
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
