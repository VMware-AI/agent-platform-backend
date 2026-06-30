package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
)

// TestIssueVirtualKey_RecordsGatewayConnection pins LLD-14 §3.2: a minted key
// persists the GatewayConnection that issued it, so its lifecycle later routes
// back to that gateway regardless of the department's *current* binding.
func TestIssueVirtualKey_RecordsGatewayConnection(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	injectFakeGatewayModels(r)
	r.GatewayKeyClientFor = func(context.Context, *ent.GatewayConnection) gateway.Client {
		return &fakeGateway{}
	}
	mr := &mutationResolver{r}

	// First gateway auto-becomes the platform default; the second is dept-specific.
	gwDefault, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw-default", Endpoint: "https://default",
	})
	if err != nil {
		t.Fatalf("register default gw: %v", err)
	}
	gwDept, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw-dept", Endpoint: "https://dept",
	})
	if err != nil {
		t.Fatalf("register dept gw: %v", err)
	}
	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{
		Name: "research", GatewayConnectionID: &gwDept.ID,
	})
	if err != nil {
		t.Fatalf("create department: %v", err)
	}

	// A key for the department records the DEPARTMENT's gateway connection id.
	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: uuid.New().String(), TeamID: dept.LitellmTeamID,
	})
	if err != nil {
		t.Fatalf("issue dept key: %v", err)
	}
	vk := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued.VirtualKey.ID))
	if vk.GatewayConnectionID == nil || *vk.GatewayConnectionID != uuid.MustParse(gwDept.ID) {
		t.Fatalf("dept key gateway_connection_id = %v, want %s", vk.GatewayConnectionID, gwDept.ID)
	}

	// A key with no department records the platform DEFAULT gateway's id.
	issued2, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("issue default key: %v", err)
	}
	vk2 := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued2.VirtualKey.ID))
	if vk2.GatewayConnectionID == nil || *vk2.GatewayConnectionID != uuid.MustParse(gwDefault.ID) {
		t.Fatalf("default key gateway_connection_id = %v, want %s", vk2.GatewayConnectionID, gwDefault.ID)
	}
}

// TestIssueVirtualKey_LegacyGatewayLeavesConnectionNil pins the fallback: with no
// DB GatewayConnection (only the legacy injected r.Gateway), a minted key has a
// NULL gateway_connection_id → its lifecycle falls back to team→department routing.
func TestIssueVirtualKey_LegacyGatewayLeavesConnectionNil(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	r.Gateway = &fakeGateway{} // legacy injected gateway, no GatewayConnection rows
	mr := &mutationResolver{r}

	issued, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}
	vk := r.Ent.VirtualKey.GetX(ctx, uuid.MustParse(issued.VirtualKey.ID))
	if vk.GatewayConnectionID != nil {
		t.Fatalf("legacy-gateway key should leave gateway_connection_id NULL, got %v", vk.GatewayConnectionID)
	}
}
