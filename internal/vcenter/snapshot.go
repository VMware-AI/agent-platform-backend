package vcenter

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// SnapshotInfo is a flattened view of one VM snapshot (LLD-03 §4 快照).
type SnapshotInfo struct {
	Name        string
	Description string
	State       string
	CreatedAt   time.Time
}

// CreateSnapshot snapshots a VM. memory=false/quiesce=false keeps it fast and
// avoids depending on guest tools (agent VMs may be mid-provision). The name is
// the handle RevertSnapshot uses.
func (c *Client) CreateSnapshot(ctx context.Context, vmName, name, description string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	task, err := vm.CreateSnapshot(ctx, name, description, false, false)
	if err != nil {
		return fmt.Errorf("vcenter: create snapshot %q on %s: %w", name, vmName, err)
	}
	return task.Wait(ctx)
}

// RevertSnapshot rolls a VM back to a named snapshot. DESTRUCTIVE: it discards
// all state since the snapshot, so callers must double-confirm (LLD-03 §4).
// suppressPowerOn=false restores the VM to the snapshot's recorded power state.
func (c *Client) RevertSnapshot(ctx context.Context, vmName, snapshotName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	task, err := vm.RevertToSnapshot(ctx, snapshotName, false)
	if err != nil {
		return fmt.Errorf("vcenter: revert %s to snapshot %q: %w", vmName, snapshotName, err)
	}
	return task.Wait(ctx)
}

// ListSnapshots returns the VM's snapshots, flattened from the snapshot tree in
// tree (parent-before-child) order. Empty slice if the VM has none.
func (c *Client) ListSnapshots(ctx context.Context, vmName string) ([]SnapshotInfo, error) {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return nil, err
	}
	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"snapshot"}, &mvm); err != nil {
		return nil, fmt.Errorf("vcenter: read snapshots %s: %w", vmName, err)
	}
	out := make([]SnapshotInfo, 0)
	if mvm.Snapshot == nil {
		return out, nil
	}
	return flattenSnapshots(out, mvm.Snapshot.RootSnapshotList), nil
}

// flattenSnapshots walks the snapshot tree depth-first into a flat slice.
func flattenSnapshots(acc []SnapshotInfo, trees []types.VirtualMachineSnapshotTree) []SnapshotInfo {
	for _, t := range trees {
		acc = append(acc, SnapshotInfo{
			Name:        t.Name,
			Description: t.Description,
			State:       string(t.State),
			CreatedAt:   t.CreateTime,
		})
		acc = flattenSnapshots(acc, t.ChildSnapshotList)
	}
	return acc
}
