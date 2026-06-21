package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// fakeVCenter is an in-memory VCenterClient for graph tests; it records Destroy
// calls, keeps an in-memory snapshot store, and no-ops the rest.
type fakeVCenter struct {
	destroyed []string
	snapshots map[string][]vcenter.SnapshotInfo
	reverted  []string
}

func (f *fakeVCenter) CloneFromTemplate(context.Context, vcenter.CloneSpec) (*vcenter.VMInfo, error) {
	return &vcenter.VMInfo{}, nil
}
func (f *fakeVCenter) ListTemplates(context.Context) ([]vcenter.VMInfo, error) { return nil, nil }
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
func (f *fakeVCenter) Logout(context.Context) error { return nil }

// rollbackDeploy must destroy the orphan VM, revoke the live gateway key, and
// mark the agent exception — so a post-Provision persistence failure leaves no
// running VM and no ungoverned key.
func TestRollbackDeploy_DestroysVMRevokesKeyMarksException(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	fg := &fakeGateway{}
	r.Gateway = fg
	ctx := context.Background()
	mr := &mutationResolver{r}

	owner, err := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "o", Email: "o@x.io", Password: "OwnerPass123", Role: model.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	seedActiveTemplate(t, r, "goose")
	ag, err := mr.CreateAgent(userCtx(owner.ID, "user"), model.CreateAgentInput{Name: "a", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	aid := uuid.MustParse(ag.ID)
	agRow := r.Ent.Agent.GetX(ctx, aid)

	fvc := &fakeVCenter{}
	r.rollbackDeploy(ctx, fvc, agRow, "vm-xyz", "sk-live-key")

	if len(fvc.destroyed) != 1 || fvc.destroyed[0] != "vm-xyz" {
		t.Errorf("VM not destroyed: %v", fvc.destroyed)
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-live-key" {
		t.Errorf("gateway key not revoked: %v", fg.deleted)
	}
	if got := r.Ent.Agent.GetX(ctx, aid); got.Status != agent.StatusException {
		t.Errorf("agent status = %s, want exception", got.Status)
	}
}
