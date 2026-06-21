package vcenter

import (
	"context"
	"testing"
)

func TestSnapshotLifecycle_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()

	vms, err := c.ListVMs(ctx)
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	vm := vms[0].Name

	// Initially none.
	snaps, err := c.ListSnapshots(ctx, vm)
	if err != nil {
		t.Fatalf("ListSnapshots (empty): %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots, got %d", len(snaps))
	}

	if err := c.CreateSnapshot(ctx, vm, "snap1", "before risky change"); err != nil {
		t.Fatalf("CreateSnapshot snap1: %v", err)
	}
	if err := c.CreateSnapshot(ctx, vm, "snap2", ""); err != nil {
		t.Fatalf("CreateSnapshot snap2: %v", err)
	}

	snaps, err = c.ListSnapshots(ctx, vm)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d: %+v", len(snaps), snaps)
	}
	byName := map[string]SnapshotInfo{}
	for _, s := range snaps {
		byName[s.Name] = s
	}
	if byName["snap1"].Description != "before risky change" {
		t.Fatalf("snap1 description not captured: %+v", byName["snap1"])
	}
	if _, ok := byName["snap2"]; !ok {
		t.Fatalf("snap2 missing from list: %+v", snaps)
	}

	if err := c.RevertSnapshot(ctx, vm, "snap1"); err != nil {
		t.Fatalf("RevertSnapshot snap1: %v", err)
	}
}

func TestSnapshot_Errors_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()

	if err := c.CreateSnapshot(ctx, "no-such-vm", "s", ""); err == nil {
		t.Fatal("CreateSnapshot on missing VM should error")
	}
	if _, err := c.ListSnapshots(ctx, "no-such-vm"); err == nil {
		t.Fatal("ListSnapshots on missing VM should error")
	}

	vms, err := c.ListVMs(ctx)
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	if err := c.RevertSnapshot(ctx, vms[0].Name, "no-such-snapshot"); err == nil {
		t.Fatal("RevertSnapshot to missing snapshot should error")
	}
}
