// Test-only helpers shared across the graph package's _test.go files.
// adminCtx / userCtx / tenantAdminCtx construct context.Context values with
// the actor the rbac test matrix expects; the canonical test Resolver builder
// is newTestResolver in integration_test.go. Centralised here so this PR's
// test cleanup (which removed deploy_test.go where these helpers originated)
// doesn't leave downstream tests uncompilable.
package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
)

func userCtx(id, role string) context.Context {
	return auth.WithCurrentUser(context.Background(), &auth.CurrentUser{ID: id, Role: auth.Role(role)})
}

func adminCtx() context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: "00000000-0000-0000-0000-000000000001", Role: auth.RoleAdmin})
}

func tenantAdminCtx(id, tenantID string) context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: id, Role: auth.RoleReadOnly, TenantID: tenantID})
}

// readOnlyCtx mirrors adminCtx / userCtx for the read_only role — the
// observability seat (LLD-15 T7) is the canonical read_only subscriber.
func readOnlyCtx() context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: "00000000-0000-0000-0000-000000000002", Role: auth.RoleReadOnly})
}
