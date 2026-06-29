package backfill

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/store"
)

func newDB(t *testing.T) (*ent.Client, func()) {
	t.Helper()
	c, err := store.Open(context.Background(), "", true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return c, func() { _ = c.Close() }
}

func mkGateway(t *testing.T, c *ent.Client, name, endpoint string, isDefault bool) *ent.GatewayConnection {
	t.Helper()
	g, err := c.GatewayConnection.Create().
		SetName(name).SetEndpoint(endpoint).SetIsDefault(isDefault).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create gateway %s: %v", name, err)
	}
	return g
}

func mkDept(t *testing.T, c *ent.Client, name string, gatewayID *uuid.UUID) *ent.Department {
	t.Helper()
	b := c.Department.Create().SetName(name)
	if gatewayID != nil {
		b.SetGatewayConnectionID(*gatewayID)
	}
	d, err := b.Save(context.Background())
	if err != nil {
		t.Fatalf("create dept %s: %v", name, err)
	}
	return d
}

func mkKey(t *testing.T, c *ent.Client, alias, teamID string, status virtualkey.Status) *ent.VirtualKey {
	t.Helper()
	b := c.VirtualKey.Create().
		SetLitellmKey("sk-" + alias).SetAlias(alias).
		SetUserID(uuid.New()).SetStatus(status)
	if teamID != "" {
		b.SetTeamID(teamID)
	}
	vk, err := b.Save(context.Background())
	if err != nil {
		t.Fatalf("create key %s: %v", alias, err)
	}
	return vk
}

func gatewayOf(t *testing.T, c *ent.Client, id uuid.UUID) *uuid.UUID {
	t.Helper()
	return c.VirtualKey.GetX(context.Background(), id).GatewayConnectionID
}

// TestVKGateway_FillsFromDeptBindingElseDefault is the core LLD-14 §3.6 backfill:
// each legacy NULL key is filled from its team→department's current gateway, else
// the platform default; keys with no resolvable team still land on the default.
func TestVKGateway_FillsFromDeptBindingElseDefault(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	gwA := mkGateway(t, db, "A", "https://A", true) // default
	gwB := mkGateway(t, db, "B", "https://B", false)
	deptA := mkDept(t, db, "deptA", &gwA.ID)
	deptB := mkDept(t, db, "deptB", &gwB.ID)

	keyB := mkKey(t, db, "to-B", deptB.ID.String(), virtualkey.StatusActive)      // → B
	keyA := mkKey(t, db, "to-A", deptA.ID.String(), virtualkey.StatusActive)      // → A (bound)
	keyNoTeam := mkKey(t, db, "no-team", "", virtualkey.StatusActive)             // → default A
	keyBadTeam := mkKey(t, db, "bad-team", "not-a-uuid", virtualkey.StatusActive) // → default A
	keyGhost := mkKey(t, db, "ghost", uuid.New().String(), virtualkey.StatusDisabled)
	// disabled is non-revoked → still backfilled; ghost dept doesn't exist → default A

	res, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("VKGateway: %v", err)
	}
	if res.Scanned != 5 || res.Filled != 5 || res.SkippedNoGateway != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want scanned=5 filled=5 skipped=0 failed=0", res)
	}

	want := map[uuid.UUID]uuid.UUID{
		keyB.ID:       gwB.ID,
		keyA.ID:       gwA.ID,
		keyNoTeam.ID:  gwA.ID,
		keyBadTeam.ID: gwA.ID,
		keyGhost.ID:   gwA.ID,
	}
	for id, wantGW := range want {
		got := gatewayOf(t, db, id)
		if got == nil || *got != wantGW {
			t.Errorf("key %v: gateway = %v, want %v", id, got, wantGW)
		}
	}
}

// TestVKGateway_DanglingDeptBindingFallsToDefault: a department bound to a gateway
// that no longer exists (no FK prevents this) must NOT persist the stale id — the
// key falls through to the platform default, so every filled id points at a live
// gateway (matches the T3 reconciler).
func TestVKGateway_DanglingDeptBindingFallsToDefault(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	gwDefault := mkGateway(t, db, "default", "https://D", true)
	ghostGatewayID := uuid.New() // never created → a dangling binding target
	dept := mkDept(t, db, "dangling", &ghostGatewayID)
	key := mkKey(t, db, "k", dept.ID.String(), virtualkey.StatusActive)

	res, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("VKGateway: %v", err)
	}
	if res.Filled != 1 {
		t.Fatalf("result = %+v, want filled=1", res)
	}
	got := gatewayOf(t, db, key.ID)
	if got == nil || *got != gwDefault.ID {
		t.Errorf("dangling-bound key should fall to default %v, got %v (must not persist ghost %v)", gwDefault.ID, got, ghostGatewayID)
	}
}

// TestVKGateway_Idempotent: a second pass finds nothing left to do.
func TestVKGateway_Idempotent(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	mkGateway(t, db, "A", "https://A", true)
	mkKey(t, db, "k1", "", virtualkey.StatusActive)
	mkKey(t, db, "k2", "", virtualkey.StatusActive)

	first, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.Filled != 2 {
		t.Fatalf("first pass filled = %d, want 2", first.Filled)
	}
	second, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Scanned != 0 || second.Filled != 0 {
		t.Errorf("second pass should be a no-op, got %+v", second)
	}
}

// TestVKGateway_SkipsWhenUnresolvable: with no default and an unbound team, the
// key stays NULL (re-runnable later) and is reported as skipped, not failed.
func TestVKGateway_SkipsWhenUnresolvable(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	// no default gateway; a non-default one exists but no dept binds the key to it
	mkGateway(t, db, "B", "https://B", false)
	orphan := mkKey(t, db, "orphan", "", virtualkey.StatusActive)

	res, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("VKGateway: %v", err)
	}
	if res.Scanned != 1 || res.Filled != 0 || res.SkippedNoGateway != 1 {
		t.Fatalf("result = %+v, want scanned=1 filled=0 skipped=1", res)
	}
	if got := gatewayOf(t, db, orphan.ID); got != nil {
		t.Errorf("unresolvable key should stay NULL, got %v", got)
	}
}

// TestVKGateway_LeavesRevokedAndAlreadySetUntouched: revoked rows are out of
// scope, and a key that already has a gateway is never re-derived.
func TestVKGateway_LeavesRevokedAndAlreadySetUntouched(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	gwA := mkGateway(t, db, "A", "https://A", true)
	gwB := mkGateway(t, db, "B", "https://B", false)

	revoked := mkKey(t, db, "revoked", "", virtualkey.StatusRevoked)
	// already bound to B — must not be rewritten to the default A
	alreadySet, err := db.VirtualKey.Create().
		SetLitellmKey("sk-set").SetUserID(uuid.New()).
		SetStatus(virtualkey.StatusActive).SetGatewayConnectionID(gwB.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create already-set key: %v", err)
	}

	res, err := VKGateway(ctx, db)
	if err != nil {
		t.Fatalf("VKGateway: %v", err)
	}
	if res.Scanned != 0 || res.Filled != 0 {
		t.Errorf("revoked + already-set rows are out of scope, got %+v", res)
	}
	if got := gatewayOf(t, db, revoked.ID); got != nil {
		t.Errorf("revoked key should stay NULL, got %v", got)
	}
	if got := gatewayOf(t, db, alreadySet.ID); got == nil || *got != gwB.ID {
		t.Errorf("already-set key must keep gateway B, got %v (default was %v)", got, gwA.ID)
	}
}
