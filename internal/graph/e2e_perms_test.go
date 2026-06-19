package graph_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
)

// Verifies @hasPermission directives enforce through the real GraphQL executor
// (resolvers are also unit-tested directly, which bypasses directives).
func TestE2E_PermissionDirectives(t *testing.T) {
	e := setupE2E(t)
	defer e.cleanup()

	userCookie := e.seedUser(t, "plain", user.RoleUser)
	obsCookie := e.seedUser(t, "obs", user.RoleObservability)
	adminCookie := e.seedUser(t, "boss", user.RoleAdmin)

	// --- audit:view --- observability has it, a plain user does not.
	const auditQ = `{ auditLogs { total } }`
	var aResp struct {
		AuditLogs struct{ Total int }
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(userCookie)); err == nil {
		t.Fatal("plain user must be denied audit:view")
	}
	if err := e.gql.Post(auditQ, &aResp, client.AddCookie(obsCookie)); err != nil {
		t.Fatalf("observability should pass audit:view: %v", err)
	}
	// unauthenticated denied
	if err := e.gql.Post(auditQ, &aResp); err == nil {
		t.Fatal("unauthenticated must be denied audit:view")
	}

	// --- route:manage --- admin has it, observability does not.
	const routeM = `mutation { upsertRateLimitPolicy(input:{name:"p"}){ id name } }`
	var rResp struct {
		UpsertRateLimitPolicy struct {
			ID   string
			Name string
		}
	}
	if err := e.gql.Post(routeM, &rResp, client.AddCookie(obsCookie)); err == nil {
		t.Fatal("observability must be denied route:manage")
	}
	if err := e.gql.Post(routeM, &rResp, client.AddCookie(adminCookie)); err != nil {
		t.Fatalf("admin should pass route:manage: %v", err)
	}
	if rResp.UpsertRateLimitPolicy.Name != "p" {
		t.Fatalf("unexpected result: %+v", rResp.UpsertRateLimitPolicy)
	}
}
