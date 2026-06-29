package reconcile

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/virtualkey"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// TestRunCycle_MultiGateway_ScansEachAgainstOwnRows is the core OQ-5 / LLD-14 T3
// regression: with several gateways, the reconciler scans EACH gateway against
// only the rows assigned to it. Two consequences must hold under Prune:
//
//  1. A key that lives on gateway B is NOT flagged stale (and revoked) while
//     gateway A is scanned — the single-gateway reconciler, which compared one
//     gateway's listing against ALL rows, would wrongly revoke it.
//  2. An orphan on gateway B is pruned at gateway B (not skipped because only the
//     default/A gateway was ever scanned).
func TestRunCycle_MultiGateway_ScansEachAgainstOwnRows(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	// vkA lives on gateway A, vkB on gateway B (both active, both governed).
	vkA := mkKey(t, db, "kA", virtualkey.StatusActive)
	vkB := mkKey(t, db, "kB", virtualkey.StatusActive)

	gwA := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "kA"}, {Key: "orphanA"}}}
	gwB := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "kB"}, {Key: "orphanB"}}}

	r := &Reconciler{
		Ent:   db,
		Prune: true,
		GatewaysFunc: func(context.Context) ([]GatewayTarget, error) {
			return []GatewayTarget{
				{Gateway: gwA, Keys: []*ent.VirtualKey{vkA}},
				{Gateway: gwB, Keys: []*ent.VirtualKey{vkB}},
			}, nil
		},
	}
	r.runCycle(ctx)

	// Each gateway pruned only its OWN orphan.
	if len(gwA.deleted) != 1 || gwA.deleted[0] != "orphanA" {
		t.Errorf("gateway A should prune only orphanA, got %v", gwA.deleted)
	}
	if len(gwB.deleted) != 1 || gwB.deleted[0] != "orphanB" {
		t.Errorf("gateway B should prune only orphanB, got %v", gwB.deleted)
	}

	// Neither governed key was revoked — the bug was vkB being seen as stale while
	// scanning gateway A.
	if got := db.VirtualKey.GetX(ctx, vkA.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("vkA wrongly revoked: %s", got.Status)
	}
	if got := db.VirtualKey.GetX(ctx, vkB.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("vkB wrongly revoked (cross-gateway false stale): %s", got.Status)
	}
}

// TestRunCycle_MultiGateway_StalePerTarget proves stale detection still works,
// but scoped to the owning gateway: within gateway B's own row set one key is
// present and another has vanished — only the vanished one is revoked, and gateway
// A's key is untouched. (B lists a matching key too, so the all-unmatched guard
// does not fire — that catastrophic-mismatch guard is covered separately.)
func TestRunCycle_MultiGateway_StalePerTarget(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	vkA := mkKey(t, db, "kA", virtualkey.StatusActive)   // present at A
	vkB1 := mkKey(t, db, "kB1", virtualkey.StatusActive) // present at B
	vkB2 := mkKey(t, db, "kB2", virtualkey.StatusActive) // assigned to B, gone at B → stale

	gwA := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "kA"}}}
	gwB := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "kB1"}}} // kB2 absent

	r := &Reconciler{
		Ent:   db,
		Prune: true,
		GatewaysFunc: func(context.Context) ([]GatewayTarget, error) {
			return []GatewayTarget{
				{Gateway: gwA, Keys: []*ent.VirtualKey{vkA}},
				{Gateway: gwB, Keys: []*ent.VirtualKey{vkB1, vkB2}},
			}, nil
		},
	}
	r.runCycle(ctx)

	if got := db.VirtualKey.GetX(ctx, vkA.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("vkA should stay active (present at its gateway), got %s", got.Status)
	}
	if got := db.VirtualKey.GetX(ctx, vkB1.ID); got.Status != virtualkey.StatusActive {
		t.Errorf("vkB1 should stay active (present at its gateway), got %s", got.Status)
	}
	if got := db.VirtualKey.GetX(ctx, vkB2.ID); got.Status != virtualkey.StatusRevoked {
		t.Errorf("vkB2 should be revoked (vanished from its own gateway), got %s", got.Status)
	}
}

// TestRunCycle_MultiGateway_SkipsNilGatewayTarget: a target whose client failed to
// build (nil) is skipped, and the remaining targets still reconcile.
func TestRunCycle_MultiGateway_SkipsNilGatewayTarget(t *testing.T) {
	db, cleanup := newDB(t)
	defer cleanup()
	ctx := context.Background()

	vkA := mkKey(t, db, "kA", virtualkey.StatusActive)
	gwA := &fakeKeyGateway{keys: []gateway.KeyInfo{{Key: "kA"}, {Key: "orphanA"}}}

	r := &Reconciler{
		Ent:   db,
		Prune: true,
		GatewaysFunc: func(context.Context) ([]GatewayTarget, error) {
			return []GatewayTarget{
				{Gateway: nil, Keys: nil}, // unbuildable client — must be skipped, not panic
				{Gateway: gwA, Keys: []*ent.VirtualKey{vkA}},
			}, nil
		},
	}
	r.runCycle(ctx)

	if len(gwA.deleted) != 1 || gwA.deleted[0] != "orphanA" {
		t.Errorf("gateway A should still reconcile after a nil target, got %v", gwA.deleted)
	}
}
