// Test-only helpers shared across the graph package's _test.go files.
// adminCtx / userCtx / tenantAdminCtx construct context.Context values with
// the actor the rbac test matrix expects; the canonical test Resolver builder
// is newTestResolver in integration_test.go. Centralised here so this PR's
// test cleanup (which removed deploy_test.go where these helpers originated)
// doesn't leave downstream tests uncompilable.
package graph

import (
	"context"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/google/uuid"
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

func ptr[T any](v T) *T { return &v }

// seedVirtualKey returns a VirtualKey builder pre-filled with every field the
// model-routing schema REQUIRES (masked_key / name / model_gateway_id) so seed
// sites only chain what the test is about (SetAgentID, SetStatus, SetModels,
// …). One home for the required set: the next required column is added here
// once, not hunted across N test files.
func seedVirtualKey(ec *ent.Client, key string) *ent.VirtualKeyCreate {
	return ec.VirtualKey.Create().SetLitellmKey(key).SetMaskedKey("sk-***").
		SetName(key).SetModelGatewayID(uuid.New())
}
