package graph

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

func userCtx(id, role string) context.Context {
	return auth.WithCurrentUser(context.Background(), &auth.CurrentUser{ID: id, Role: auth.Role(role)})
}

func adminCtx() context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: "00000000-0000-0000-0000-000000000001", Role: auth.RoleAdmin})
}

func tenantAdminCtx(id, tenantID string) context.Context {
	return auth.WithCurrentUser(context.Background(),
		&auth.CurrentUser{ID: id, Role: auth.RoleTenantAdmin, TenantID: tenantID})
}

func TestDeployAgent_EndToEnd(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	// vcsim acting as the resource pool's vCenter
	mdl := simulator.VPX()
	if err := mdl.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	vsrv := mdl.Service.NewServer()
	defer vsrv.Close()
	defer mdl.Remove()

	// wire deploy dependencies into the resolver
	r.Gateway = &fakeGateway{}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc1": {Username: "svc", Password: "pw"},
	})
	r.GatewayURL = "https://gw.internal"
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}

	mr := &mutationResolver{r}

	// create owner + agent + pool (pool endpoint points at vcsim)
	owner := mkUser(t, mr, ctx, "deployer", "d@x.io", model.RoleNameUser)
	ownerCtx := userCtx(owner.ID, "user")
	seedActiveTemplate(t, mr.Resolver, "goose")

	ag, err := mr.CreateAgent(ownerCtx, model.CreateAgentInput{Name: "alice-goose", AgentType: "goose"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ref := "vault://oc1"
	pool, err := mr.RegisterResourcePool(adminCtx(), model.RegisterResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("RegisterResourcePool: %v", err)
	}

	// pick a vcsim VM to target
	vc, _ := vcenter.Connect(ctx, vsrv.URL.String(), "svc", "pw", true)
	vms, _ := vc.ListVMs(ctx)
	_ = vc.Logout(ctx)
	if len(vms) == 0 {
		t.Fatal("no vcsim vms")
	}

	hostname := "agent-vm-01"
	dep, err := mr.DeployAgent(ownerCtx, model.DeployAgentInput{
		AgentID: ag.ID, Template: vms[0].Name, VMName: "agent-alice-deployed",
		ResourcePoolID: pool.ID, Hostname: &hostname,
	})
	if err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	if dep.VirtualKeySecret != "sk-fake-123" {
		t.Fatalf("secret = %q", dep.VirtualKeySecret)
	}
	if dep.Agent.Status != model.AgentStatusRunning {
		t.Fatalf("agent status = %v, want running", dep.Agent.Status)
	}
	if dep.Agent.Endpoint == nil || *dep.Agent.Endpoint != "agent-alice-deployed" {
		t.Fatalf("endpoint (vm_ref) not set: %+v", dep.Agent.Endpoint)
	}

	// a virtual key row was persisted for the owner
	keys, _ := (&queryResolver{r}).VirtualKeys(ctx, &owner.ID)
	if len(keys) != 1 {
		t.Fatalf("expected 1 virtual key, got %d", len(keys))
	}
	// the deploy path persists the gateway's reconciliation token (else the
	// reconciler would later flag this live key's row as stale).
	kid := uuid.MustParse(keys[0].ID)
	if tok := r.Ent.VirtualKey.GetX(ctx, kid).LitellmToken; tok != "tok-fake-123" {
		t.Fatalf("deploy did not persist litellm_token: %q", tok)
	}
}

func TestVMTemplates_VCSim(t *testing.T) {
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

	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	ref := "vault://oc"
	pool, err := (&mutationResolver{r}).RegisterResourcePool(adminCtx(), model.RegisterResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	qr := &queryResolver{r}

	// no templates until one is marked
	if tpls, err := qr.VMTemplates(adminCtx(), pool.ID); err != nil || len(tpls) != 0 {
		t.Fatalf("expected 0 templates, got %d err=%v", len(tpls), err)
	}

	vc, _ := vcenter.Connect(ctx, vsrv.URL.String(), "u", "p", true)
	vms, _ := vc.ListVMs(ctx)
	if err := vc.PowerOff(ctx, vms[0].Name); err != nil {
		t.Fatalf("poweroff: %v", err)
	}
	if err := vc.MarkAsTemplate(ctx, vms[0].Name); err != nil {
		t.Fatalf("mark template: %v", err)
	}
	_ = vc.Logout(ctx)

	tpls, err := qr.VMTemplates(adminCtx(), pool.ID)
	if err != nil {
		t.Fatalf("VMTemplates: %v", err)
	}
	if len(tpls) != 1 || tpls[0].Name != vms[0].Name {
		t.Fatalf("expected template %q, got %+v", vms[0].Name, tpls)
	}
}

func TestVMTemplates_AdminOnly(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// A non-admin is rejected before any vCenter connection is attempted.
	_, err := (&queryResolver{r}).VMTemplates(userCtx("11111111-1111-1111-1111-111111111111", "user"),
		"00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("non-admin should be forbidden from listing vm templates")
	}
}

func TestRecycleAgent_VCSim(t *testing.T) {
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

	fg := &fakeGateway{}
	r.Gateway = fg
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc": {Username: "u", Password: "p"},
	})
	r.GatewayURL = "https://gw.internal"
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}
	mr := &mutationResolver{r}

	owner := mkUser(t, mr, ctx, "recycler", "r@x.io", model.RoleNameUser)
	ownerCtx := userCtx(owner.ID, "user")
	seedActiveTemplate(t, mr.Resolver, "goose")
	ag, _ := mr.CreateAgent(ownerCtx, model.CreateAgentInput{Name: "bob-goose", AgentType: "goose"})
	ref := "vault://oc"
	pool, _ := mr.RegisterResourcePool(adminCtx(), model.RegisterResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})

	vc, _ := vcenter.Connect(ctx, vsrv.URL.String(), "u", "p", true)
	vms, _ := vc.ListVMs(ctx)
	_ = vc.Logout(ctx)
	if _, err := mr.DeployAgent(ownerCtx, model.DeployAgentInput{
		AgentID: ag.ID, Template: vms[0].Name, VMName: "bob-vm", ResourcePoolID: pool.ID,
	}); err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}

	// confirm=false is rejected (destructive)
	if _, err := mr.RecycleAgent(ownerCtx, model.RecycleAgentInput{AgentID: ag.ID, Confirm: false}); err == nil {
		t.Fatal("recycle without confirm should fail")
	}

	out, err := mr.RecycleAgent(ownerCtx, model.RecycleAgentInput{AgentID: ag.ID, Confirm: true})
	if err != nil {
		t.Fatalf("RecycleAgent: %v", err)
	}
	if out.Status != model.AgentStatusStopped {
		t.Fatalf("status = %v, want stopped", out.Status)
	}

	// the cloned VM is gone from vcsim and the key was revoked
	vc2, _ := vcenter.Connect(ctx, vsrv.URL.String(), "u", "p", true)
	after, _ := vc2.ListVMs(ctx)
	_ = vc2.Logout(ctx)
	for _, vm := range after {
		if vm.Name == "bob-vm" {
			t.Fatal("agent VM not destroyed on recycle")
		}
	}
	if len(fg.deleted) == 0 {
		t.Fatal("agent key not revoked on recycle")
	}
}
