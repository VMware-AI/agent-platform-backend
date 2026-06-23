package main

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/user"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func TestSeedAdmin(t *testing.T) {
	ctx := context.Background()

	// NB: store.Open("") is a NAMED shared-cache in-memory sqlite, so all open
	// clients share one DB; close each before opening the next to isolate cases.

	// An explicitly-set bootstrap password → admin is usable immediately (the
	// operator already chose a credential; no forced first-login change).
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "OperatorChosen123!")
	c1, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := seedAdmin(ctx, c1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := c1.User.Query().Where(user.Username("admin")).OnlyX(ctx)
	if a.Role != user.RoleAdmin {
		t.Fatalf("role = %s, want admin (super admin)", a.Role)
	}
	if a.MustChangePassword {
		t.Fatal("explicit bootstrap password should NOT force a change")
	}
	if a.Email != "admin@platform.local" {
		t.Fatalf("email = %s", a.Email)
	}
	// Idempotent: a second call on a non-empty DB creates no second admin.
	if err := seedAdmin(ctx, c1); err != nil {
		t.Fatalf("seed (idempotent): %v", err)
	}
	if n := c1.User.Query().CountX(ctx); n != 1 {
		t.Fatalf("want exactly 1 user, got %d", n)
	}
	_ = c1.Close() // drop the shared in-memory DB so the next case starts empty

	// The insecure dev default (no env) DOES force a first-login change.
	t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", "")
	c2, err := store.Open(ctx, "", true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c2.Close()
	if err := seedAdmin(ctx, c2); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	d := c2.User.Query().Where(user.Username("admin")).OnlyX(ctx)
	if !d.MustChangePassword {
		t.Fatal("dev-default bootstrap password should force a change")
	}
}
