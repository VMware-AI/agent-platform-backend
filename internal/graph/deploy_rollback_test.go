package graph

import (
	"context"
	"fmt"

	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// fakeVCenter is an in-memory VCenterClient for graph tests; it records Destroy
// calls, keeps an in-memory snapshot store, and no-ops the rest.
type fakeVCenter struct {
	destroyed []string
	snapshots map[string][]vcenter.SnapshotInfo
	reverted  []string
	logouts   int
}

func (f *fakeVCenter) CloneFromTemplate(context.Context, vcenter.CloneSpec) (*vcenter.VMInfo, error) {
	return &vcenter.VMInfo{}, nil
}
func (f *fakeVCenter) ListTemplates(context.Context) ([]vcenter.VMInfo, error) { return nil, nil }
func (f *fakeVCenter) ListResourcePools(context.Context) ([]vcenter.ResourcePoolInfo, error) {
	return nil, nil
}
func (f *fakeVCenter) SetGuestinfo(context.Context, string, map[string]string) error {
	return nil
}
func (f *fakeVCenter) PowerOn(context.Context, string) error { return nil }
func (f *fakeVCenter) Destroy(_ context.Context, vmName string) error {
	f.destroyed = append(f.destroyed, vmName)
	return nil
}
func (f *fakeVCenter) Inventory(context.Context) (vcenter.Inventory, error) {
	return vcenter.Inventory{}, nil
}
func (f *fakeVCenter) CreateSnapshot(_ context.Context, vmName, name, description string) error {
	if f.snapshots == nil {
		f.snapshots = map[string][]vcenter.SnapshotInfo{}
	}
	f.snapshots[vmName] = append(f.snapshots[vmName], vcenter.SnapshotInfo{Name: name, Description: description, State: "poweredOn"})
	return nil
}
func (f *fakeVCenter) RevertSnapshot(_ context.Context, vmName, snapshotName string) error {
	for _, s := range f.snapshots[vmName] {
		if s.Name == snapshotName {
			f.reverted = append(f.reverted, vmName+"/"+snapshotName)
			return nil
		}
	}
	return fmt.Errorf("snapshot %q not found", snapshotName)
}
func (f *fakeVCenter) ListSnapshots(_ context.Context, vmName string) ([]vcenter.SnapshotInfo, error) {
	return f.snapshots[vmName], nil
}
func (f *fakeVCenter) ListContentLibraries(context.Context) ([]string, error) {
	return []string{"tkg", "iso"}, nil
}
func (f *fakeVCenter) About() vcenter.AboutInfo {
	return vcenter.AboutInfo{Version: "8.0.0", Build: "0", FullName: "VMware vCenter Server (fake)"}
}
func (f *fakeVCenter) Logout(context.Context) error { f.logouts++; return nil }
