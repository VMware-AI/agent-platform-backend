package graph

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// deployTestAgent spins vcsim, wires deps, deploys an agent and returns the
// resolver, the owner's context, and the deployed agent id.
func deployTestAgent(t *testing.T, r *Resolver) (context.Context, string) {
	t.Helper()
	ctx := context.Background()

	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	vsrv := mdl.Service.NewServer()
	t.Cleanup(func() { vsrv.Close(); mdl.Remove() })

	r.Gateway = &fakeGateway{}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.GatewayURL = "https://gw.internal"
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	mr := &mutationResolver{r}

	owner := mkUser(t, mr, ctx, "snapper", "s@x.io", model.RoleNameUser)
	ownerCtx := userCtx(owner.ID, "user")
	ref := "vault://oc"
	createdPool, _ := mr.CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	pool := createdPool.Pool

	vc, _ := vcenter.Connect(ctx, vsrv.URL.String(), "u", "p", true)
	vms, _ := vc.ListVMs(ctx)
	_ = vc.Logout(ctx)
	familyID, versionID := seedOvaFamilyVersion(t, r, "goose", vms[0].Name)
	dep, err := mr.DeployAgent(ownerCtx, model.DeployAgentInput{
		Name: "snap-vm", TemplateFamilyID: familyID, TemplateVersionID: versionID, ResourcePoolID: pool.ID,
	})
	if err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	return ownerCtx, dep.Agent.ID
}

func TestSnapshotAgent_Lifecycle_VCSim(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ownerCtx, agentID := deployTestAgent(t, r)
	mr := &mutationResolver{r}
	qr := &queryResolver{r}

	desc := "before risky change"
	snap, err := mr.SnapshotAgent(ownerCtx, model.SnapshotAgentInput{
		AgentID: agentID, Name: "pre-change", Description: &desc,
	})
	if err != nil {
		t.Fatalf("SnapshotAgent: %v", err)
	}
	if snap.Name != "pre-change" {
		t.Fatalf("snapshot name = %q", snap.Name)
	}

	snaps, err := qr.AgentSnapshots(ownerCtx, agentID)
	if err != nil {
		t.Fatalf("AgentSnapshots: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != "pre-change" {
		t.Fatalf("expected 1 snapshot pre-change, got %+v", snaps)
	}
	if snaps[0].Description == nil || *snaps[0].Description != desc {
		t.Fatalf("description not captured: %+v", snaps[0])
	}

	// revert without confirm is rejected (destructive)
	if _, err := mr.RevertAgentSnapshot(ownerCtx, model.RevertAgentSnapshotInput{
		AgentID: agentID, SnapshotName: "pre-change", Confirm: false,
	}); err == nil {
		t.Fatal("revert without confirm should fail")
	}

	ok, err := mr.RevertAgentSnapshot(ownerCtx, model.RevertAgentSnapshotInput{
		AgentID: agentID, SnapshotName: "pre-change", Confirm: true,
	})
	if err != nil || !ok {
		t.Fatalf("RevertAgentSnapshot: ok=%v err=%v", ok, err)
	}
}

func TestSnapshotAgent_NotOwnerDenied_VCSim(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	_, agentID := deployTestAgent(t, r)
	mr := &mutationResolver{r}

	// A different non-admin user must not snapshot someone else's agent
	// (getOwnedAgent returns a 404-style error — no existence oracle).
	stranger := userCtx("22222222-2222-2222-2222-222222222222", "user")
	if _, err := mr.SnapshotAgent(stranger, model.SnapshotAgentInput{AgentID: agentID, Name: "x"}); err == nil {
		t.Fatal("non-owner should be denied snapshotAgent")
	}
}
