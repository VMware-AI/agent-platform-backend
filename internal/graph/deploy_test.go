package graph

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/internal/auth"
	"github.com/VMware-AI/agent-platform-backend/internal/deploy"
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
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (deploy.GuestinfoSetter, error) {
		return vcenter.Connect(ctx, endpoint, user, pass, insecure)
	}

	mr := &mutationResolver{r}

	// create owner + agent + pool (pool endpoint points at vcsim)
	owner, _ := mr.CreateUser(ctx, model.CreateUserInput{
		Username: "deployer", Email: "d@x.io", Password: "DeployPass12", Role: model.RoleUser,
	})
	ownerCtx := userCtx(owner.ID, "user")

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
		AgentID: ag.ID, VMName: vms[0].Name, ResourcePoolID: pool.ID, Hostname: &hostname,
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
	if dep.Agent.VMRef == nil || *dep.Agent.VMRef != vms[0].Name {
		t.Fatalf("vm_ref not set: %+v", dep.Agent.VMRef)
	}

	// a virtual key row was persisted for the owner
	keys, _ := (&queryResolver{r}).VirtualKeys(ctx, &owner.ID)
	if len(keys) != 1 {
		t.Fatalf("expected 1 virtual key, got %d", len(keys))
	}
}
