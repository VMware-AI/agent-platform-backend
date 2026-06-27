package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/gatewayconnection"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// #5: the platform must have AT MOST one default gateway. Registering a second
// default clears the first (resolver clears-then-creates in a txn), and the DB
// partial unique index backstops the invariant. Verifies the singleton end state.
func TestRegisterGatewayConnection_DefaultSingleton(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	ctx := context.Background()
	yes := true

	g1, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "g1", Endpoint: "http://a:4000", IsDefault: &yes})
	if err != nil {
		t.Fatalf("register g1: %v", err)
	}
	if !g1.IsDefault {
		t.Fatal("first default should be set")
	}
	g2, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "g2", Endpoint: "http://b:4000", IsDefault: &yes})
	if err != nil {
		t.Fatalf("register g2: %v", err)
	}
	if !g2.IsDefault {
		t.Fatal("new default should be set")
	}

	// Exactly one default across the fleet, and it's g2 (g1 was cleared).
	n, err := r.Ent.GatewayConnection.Query().Where(gatewayconnection.IsDefault(true)).Count(ctx)
	if err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 default gateway, got %d", n)
	}
	if r.Ent.GatewayConnection.GetX(ctx, uuid.MustParse(g1.ID)).IsDefault {
		t.Fatal("g1 must be cleared when g2 became default")
	}
}

// The first-ever connection auto-defaults even without an explicit request, so
// the platform always has a default once one exists.
func TestRegisterGatewayConnection_FirstAutoDefaults(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	mr := &mutationResolver{r}
	ctx := context.Background()

	g, err := mr.RegisterGatewayConnection(ctx, model.RegisterGatewayConnectionInput{Name: "only", Endpoint: "http://a:4000"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !g.IsDefault {
		t.Fatal("the first-ever gateway should auto-default")
	}
}

// The DB partial unique index — not just the resolver's clear-first — enforces
// the singleton: a second default inserted directly (bypassing the resolver) must
// be rejected. This is the backstop for the concurrent-register race.
func TestGatewayDefault_IndexRejectsSecondDefault(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := r.Ent.GatewayConnection.Create().
		SetName("g1").SetEndpoint("http://a:4000").SetIsDefault(true).Save(ctx); err != nil {
		t.Fatalf("first default: %v", err)
	}
	_, err := r.Ent.GatewayConnection.Create().
		SetName("g2").SetEndpoint("http://b:4000").SetIsDefault(true).Save(ctx)
	if !ent.IsConstraintError(err) {
		t.Fatalf("a second default gateway must violate the partial unique index, got err=%v", err)
	}
}
