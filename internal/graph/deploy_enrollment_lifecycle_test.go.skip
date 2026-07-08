package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/ent/agentenrollment"
	"github.com/VMware-AI/agent-platform-backend/internal/agentmgr"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// cloneFailVCenter is a fakeVCenter whose CloneFromTemplate errors, so a deploy
// fails INSIDE Provision (after the resolver has already issued the enrollment).
// Mirrors the deploy-failure shape used by the rollback tests but lets us drive
// the failure with AgentMgr wired in (the vcsim-based recycle test runs with
// AgentMgr=nil, so the enrollment gap is invisible there).
type cloneFailVCenter struct {
	fakeVCenter
}

func (f *cloneFailVCenter) CloneFromTemplate(context.Context, vcenter.CloneSpec) (*vcenter.VMInfo, error) {
	return nil, errors.New("clone template: boom")
}

// Bug #6: RecycleAgent must revoke the agent-manager enrollment so the destroyed
// VM's long-lived bearer token stops authenticating against the daemon control
// plane. Before recycle the (active) enrollment authenticates; after recycle it
// must be revoked and Authenticate must fail. WITHOUT the fix (no Revoke in
// RecycleAgent) the enrollment stays active and Authenticate keeps succeeding.
func TestRecycleAgent_RevokesEnrollment(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	vsrv := mdl.Service.NewServer()
	defer vsrv.Close()
	defer mdl.Remove()

	r.Gateway = &fakeGateway{}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.GatewayURL = "https://gw.internal"
	r.AgentMgr = &agentmgr.Service{Ent: r.Ent, Secrets: secrets.NewStaticResolver(nil)}
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	mr := &mutationResolver{r}

	owner := mkUser(t, mr, ctx, "recycler", "rr@x.io", model.RoleNameUser)
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
		Name: "rev-vm", TemplateFamilyID: familyID, TemplateVersionID: versionID, ResourcePoolID: pool.ID,
	})
	if err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	ag := dep.Agent
	if ag.Endpoint == nil {
		t.Fatal("deployed agent has no vm_ref")
	}
	vmID := *ag.Endpoint // == uniqueVMName(name, agent.ID) == the enrollment's vm_id
	aid := uuid.MustParse(ag.ID)

	// DeployAgent issued a PENDING enrollment; drive it to ACTIVE (as the daemon's
	// first-boot enroll would) so we have a live VM bearer token to authenticate.
	// Re-issue to obtain a plaintext enroll token for the same vm_id, then exchange.
	enrollTok, err := r.AgentMgr.IssueEnrollment(ctx, aid, vmID, nil)
	if err != nil {
		t.Fatalf("IssueEnrollment: %v", err)
	}
	vmToken, err := r.AgentMgr.Enroll(ctx, vmID, enrollTok)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	// Pre-condition: the active enrollment authenticates (the credential is live).
	if _, err := r.AgentMgr.Authenticate(ctx, vmID, vmToken); err != nil {
		t.Fatalf("pre-recycle Authenticate should succeed, got %v", err)
	}

	if _, err := mr.RecycleAgent(ownerCtx, model.RecycleAgentInput{AgentID: ag.ID, Confirm: true}); err != nil {
		t.Fatalf("RecycleAgent: %v", err)
	}

	// Post-condition #1: the destroyed VM's bearer token no longer authenticates.
	if _, err := r.AgentMgr.Authenticate(ctx, vmID, vmToken); !errors.Is(err, agentmgr.ErrAuth) {
		t.Fatalf("post-recycle Authenticate must fail with ErrAuth, got %v (enrollment not revoked?)", err)
	}
	// Post-condition #2: the enrollment row is explicitly revoked.
	enr, err := r.Ent.AgentEnrollment.Query().Where(agentenrollment.AgentID(aid)).Only(ctx)
	if err != nil {
		t.Fatalf("query enrollment after recycle: %v", err)
	}
	if enr.Status != agentenrollment.StatusRevoked {
		t.Fatalf("enrollment status = %q after recycle, want revoked", enr.Status)
	}
}

// Bug #7: a deploy that fails AFTER IssueEnrollment must not leak the pending
// AgentEnrollment row (agent_id is a soft reference — deleting the agent row does
// not cascade). With AgentMgr wired and a vCenter whose clone fails, DeployAgent
// errors and rolls back; NO enrollment row may remain for that agent id. WITHOUT
// the fix the orphan pending enrollment lingers.
func TestDeployAgent_FailureCleansEnrollment(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	r.Gateway = &fakeGateway{}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc1": {Username: "svc", Password: "pw"},
	})
	r.GatewayURL = "https://gw.internal"
	r.AgentMgr = &agentmgr.Service{Ent: r.Ent, Secrets: secrets.NewStaticResolver(nil)}
	// Clone fails → Provision returns an error → DeployAgent runs deleteAgentRow.
	r.VCenterConnect = func(context.Context, string, string, string, bool) (VCenterClient, error) {
		return &cloneFailVCenter{}, nil
	}
	mr := &mutationResolver{r}

	owner := mkUser(t, mr, ctx, "deployfail", "df@x.io", model.RoleNameUser)
	ref := "vault://oc1"
	createdPool, err := mr.CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: "https://vc.example", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("CreateResourcePool: %v", err)
	}
	familyID, versionID := seedOvaFamilyVersion(t, r, "goose", "tmpl-src")

	_, err = mr.DeployAgent(userCtx(owner.ID, "user"), model.DeployAgentInput{
		Name: "doomed", TemplateFamilyID: familyID, TemplateVersionID: versionID, ResourcePoolID: createdPool.Pool.ID,
	})
	if err == nil {
		t.Fatal("expected DeployAgent to fail on clone")
	}

	// The agent row is gone (existing compensation) AND so is its enrollment:
	// no orphan pending enrollment may remain in the table.
	if n := r.Ent.AgentEnrollment.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected 0 orphan enrollment rows after failed deploy, got %d", n)
	}
}

func (c *cloneFailVCenter) DeployOVF(_ context.Context, _ vcenter.OVFDeploySpec) (*vcenter.VMInfo, error) {
	return nil, fmt.Errorf("not implemented")
}
