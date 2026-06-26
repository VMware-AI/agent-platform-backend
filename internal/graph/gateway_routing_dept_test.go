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

// TestGatewayRouting_PerDepartment pins LLD-13 §3.3: litellm key/team ops route to
// the gateway hosting the caller's department (its gateway_connection_id), or the
// platform default when there is no department context.
func TestGatewayRouting_PerDepartment(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	injectFakeGatewayModels(r)

	// Capture which gateway endpoint each key/team op is routed to.
	var routedTo string
	r.GatewayKeyClientFor = func(_ context.Context, g *ent.GatewayConnection) gateway.Client {
		routedTo = g.Endpoint
		return &fakeGateway{}
	}
	mr := &mutationResolver{r}

	// First gateway auto-becomes the platform default; the second is dept-specific.
	if _, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw-default", Endpoint: "https://default",
	}); err != nil {
		t.Fatalf("register default gw: %v", err)
	}
	gwDept, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{
		Name: "gw-dept", Endpoint: "https://dept",
	})
	if err != nil {
		t.Fatalf("register dept gw: %v", err)
	}

	// A department bound to gw-dept → its litellm team is created on gw-dept.
	routedTo = ""
	dept, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{
		Name: "research", GatewayConnectionID: &gwDept.ID,
	})
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	if routedTo != "https://dept" {
		t.Fatalf("department team must be created on its gateway, routed to %q", routedTo)
	}

	// A virtual key for the department's team routes to gw-dept.
	routedTo = ""
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: uuid.New().String(), TeamID: dept.LitellmTeamID,
	}); err != nil {
		t.Fatalf("issue dept key: %v", err)
	}
	if routedTo != "https://dept" {
		t.Fatalf("dept key must route to the dept gateway, routed to %q", routedTo)
	}

	// A virtual key with no team routes to the platform default.
	routedTo = ""
	if _, err := mr.IssueVirtualKey(ctx, model.IssueVirtualKeyInput{
		UserID: uuid.New().String(),
	}); err != nil {
		t.Fatalf("issue default key: %v", err)
	}
	if routedTo != "https://default" {
		t.Fatalf("teamless key must route to the default gateway, routed to %q", routedTo)
	}
}

// TestGatewayRouting_DefaultSingleton pins the is_default singleton invariant:
// registering a new default clears the flag on the previous one.
func TestGatewayRouting_DefaultSingleton(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	injectFakeGatewayModels(r)
	mr := &mutationResolver{r}

	a, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "a", Endpoint: "https://a"})
	if err != nil {
		t.Fatalf("register a: %v", err)
	}
	if !a.IsDefault {
		t.Fatal("first gateway must auto-default")
	}
	yes := true
	b, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "b", Endpoint: "https://b", IsDefault: &yes})
	if err != nil {
		t.Fatalf("register b: %v", err)
	}
	if !b.IsDefault {
		t.Fatal("explicit isDefault must take effect")
	}
	// a is no longer default (singleton).
	if g, _ := r.defaultGateway(ctx); g == nil || g.Name != "b" {
		t.Fatalf("default must be the single newest, got %v", g)
	}
}

// TestDeleteGatewayConnection_Guards pins H2: a gateway that is the default, or is
// referenced by a department, cannot be deleted (no orphaned routing).
func TestDeleteGatewayConnection_Guards(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	r.Secrets = secrets.NewStaticResolver(nil)
	injectFakeGatewayModels(r)
	r.GatewayKeyClientFor = func(context.Context, *ent.GatewayConnection) gateway.Client { return &fakeGateway{} }
	mr := &mutationResolver{r}

	def, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "def", Endpoint: "https://def"})
	if err != nil {
		t.Fatalf("register def: %v", err)
	}
	other, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "other", Endpoint: "https://other"})
	if err != nil {
		t.Fatalf("register other: %v", err)
	}
	if _, err := mr.DeleteGatewayConnection(ctx, def.ID); err == nil {
		t.Fatal("deleting the default gateway must be rejected")
	}
	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "d", GatewayConnectionID: &other.ID}); err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if _, err := mr.DeleteGatewayConnection(ctx, other.ID); err == nil {
		t.Fatal("deleting a department-referenced gateway must be rejected")
	}
}

// TestCreateDepartment_RejectsUnknownGateway pins M2: a well-formed but
// non-existent gatewayConnectionId is rejected (no dangling binding).
func TestCreateDepartment_RejectsUnknownGateway(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := adminCtx()
	mr := &mutationResolver{r}
	bogus := uuid.New().String()
	if _, err := mr.CreateDepartment(ctx, model.CreateDepartmentInput{Name: "d", GatewayConnectionID: &bogus}); err == nil {
		t.Fatal("unknown gatewayConnectionId must be rejected")
	}
}
