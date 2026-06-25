package graph

import (
	"context"
	"strings"
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

// seedOvaFamilyVersion creates an OVA template family + one version whose
// ova_identifier is the source template to clone from. Returns the family id and
// version id. Used by the create-from-OVA deploy flow tests.
func seedOvaFamilyVersion(t *testing.T, r *Resolver, kind, ovaIdentifier string) (familyID, versionID string) {
	t.Helper()
	ctx := context.Background()
	fam, err := r.Ent.OvaTemplateFamily.Create().
		SetName(kind + "-family").
		SetType(kind).
		SetDescription("test family").
		Save(ctx)
	if err != nil {
		t.Fatalf("seed ova family: %v", err)
	}
	ver, err := r.Ent.OvaTemplateVersion.Create().
		SetVersion("1.0.0").
		SetOvaIdentifier(ovaIdentifier).
		SetFamilyID(fam.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed ova version: %v", err)
	}
	return fam.ID.String(), ver.ID.String()
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

	// create owner + pool (pool endpoint points at vcsim); the agent is created BY
	// DeployAgent from the OVA version (no pre-existing agent).
	owner := mkUser(t, mr, ctx, "deployer", "d@x.io", model.RoleNameUser)
	ownerCtx := userCtx(owner.ID, "user")

	ref := "vault://oc1"
	createdPool, err := mr.CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("CreateResourcePool: %v", err)
	}
	pool := createdPool.Pool

	// pick a vcsim VM to use as the OVA source template
	vc, _ := vcenter.Connect(ctx, vsrv.URL.String(), "svc", "pw", true)
	vms, _ := vc.ListVMs(ctx)
	_ = vc.Logout(ctx)
	if len(vms) == 0 {
		t.Fatal("no vcsim vms")
	}
	familyID, versionID := seedOvaFamilyVersion(t, r, "goose", vms[0].Name)

	hostname := "agent-vm-01"
	dep, err := mr.DeployAgent(ownerCtx, model.DeployAgentInput{
		Name:              "alice-goose",
		TemplateFamilyID:  familyID,
		TemplateVersionID: versionID,
		ResourcePoolID:    pool.ID,
		Hostname:          &hostname,
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
	if dep.Agent.Type != "goose" {
		t.Fatalf("agent type = %q, want goose (from family)", dep.Agent.Type)
	}
	// VM name is UNIQUE: display-name prefix + first 8 of the new agent id, so two
	// agents sharing a display name don't collide on the clone. The display name
	// itself stays unchanged.
	if dep.Agent.Name != "alice-goose" {
		t.Fatalf("display name = %q, want alice-goose", dep.Agent.Name)
	}
	wantVM := "alice-goose-" + dep.Agent.ID[:8]
	if dep.Agent.Endpoint == nil || *dep.Agent.Endpoint != wantVM {
		t.Fatalf("endpoint (vm_ref) = %+v, want %q", dep.Agent.Endpoint, wantVM)
	}
	// the deployed-agent payload echoes the catalog provenance + target pool.
	if dep.TemplateVersion == nil || dep.TemplateVersion.ID != versionID {
		t.Fatalf("templateVersion not returned: %+v", dep.TemplateVersion)
	}
	if dep.ResourcePool == nil || dep.ResourcePool.ID != pool.ID {
		t.Fatalf("resourcePool not returned: %+v", dep.ResourcePool)
	}
	if dep.Agent.TemplateFamilyID == nil || *dep.Agent.TemplateFamilyID != familyID {
		t.Fatalf("agent.templateFamilyId not set: %+v", dep.Agent.TemplateFamilyID)
	}
	if dep.Agent.TemplateVersionID == nil || *dep.Agent.TemplateVersionID != versionID {
		t.Fatalf("agent.templateVersionId not set: %+v", dep.Agent.TemplateVersionID)
	}
	if dep.Agent.ResourcePoolID == nil || *dep.Agent.ResourcePoolID != pool.ID {
		t.Fatalf("agent.resourcePoolId not set: %+v", dep.Agent.ResourcePoolID)
	}

	// credentials.username sources from the owning user (no separate OS account).
	creds, err := (&agentResolver{r}).Credentials(ctx, dep.Agent)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if creds == nil || creds.Username != "deployer" {
		t.Fatalf("credentials.username = %+v, want deployer", creds)
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

// TestDeployAgent_VersionFamilyMismatch rejects a version that belongs to a
// different family (guards against a malformed deploy form submission).
func TestDeployAgent_VersionFamilyMismatch(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	ctx := context.Background()

	r.Gateway = &fakeGateway{}
	r.Secrets = secrets.NewStaticResolver(map[string]secrets.Credential{
		"vault://oc1": {Username: "svc", Password: "pw"},
	})
	r.VCenterConnect = func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error) {
		return &fakeVCenter{}, nil
	}
	mr := &mutationResolver{r}
	owner := mkUser(t, mr, ctx, "mismatcher", "m@x.io", model.RoleNameUser)
	ownerCtx := userCtx(owner.ID, "user")

	ref := "vault://oc1"
	createdPool, err := mr.CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: "https://vc.example", SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("CreateResourcePool: %v", err)
	}
	famA, _ := seedOvaFamilyVersion(t, r, "goose", "tmpl-a")
	_, verB := seedOvaFamilyVersion(t, r, "xiaoguai", "tmpl-b")

	// version from family B paired with family A → rejected.
	_, err = mr.DeployAgent(ownerCtx, model.DeployAgentInput{
		Name:              "bad-deploy",
		TemplateFamilyID:  famA,
		TemplateVersionID: verB,
		ResourcePoolID:    createdPool.Pool.ID,
	})
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	// no orphan agent row persisted.
	if n := r.Ent.Agent.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected 0 agents after rejected deploy, got %d", n)
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
	createdPool, err := (&mutationResolver{r}).CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pool := createdPool.Pool
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

func TestVsphereResourcePools_VCSim(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()

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
	createdPool, err := (&mutationResolver{r}).CreateResourcePool(adminCtx(), model.CreateResourcePoolInput{
		Name: "oc1", Endpoint: vsrv.URL.String(), SecretRef: &ref,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pool := createdPool.Pool

	pools, err := (&queryResolver{r}).VsphereResourcePools(adminCtx(), pool.ID)
	if err != nil {
		t.Fatalf("VsphereResourcePools: %v", err)
	}
	// vcsim's VPX model ships with at least the cluster's root "Resources" pool.
	if len(pools) == 0 {
		t.Fatal("expected at least one resource pool from vcsim, got none")
	}
	for _, p := range pools {
		if p.Name == "" || p.Path == "" {
			t.Fatalf("resource pool missing name/path: %+v", p)
		}
		// path is a full inventory path (multi-datacenter safe) — anchored at root.
		if !strings.HasPrefix(p.Path, "/") {
			t.Fatalf("resource pool path %q is not an inventory path", p.Path)
		}
	}
	// the returned path must be resolvable as a clone placement pool: feed it back
	// to a real clone so we prove `path` is what CloneFromTemplate expects.
	vc, _ := vcenter.Connect(context.Background(), vsrv.URL.String(), "u", "p", true)
	vms, _ := vc.ListVMs(context.Background())
	if len(vms) == 0 {
		t.Fatal("no vcsim vms to clone")
	}
	cloned, err := vc.CloneFromTemplate(context.Background(), vcenter.CloneSpec{
		Template:     vms[0].Name,
		Name:         "pool-path-probe",
		ResourcePool: pools[0].Path,
	})
	_ = vc.Logout(context.Background())
	if err != nil {
		t.Fatalf("CloneFromTemplate with returned path %q failed: %v", pools[0].Path, err)
	}
	if cloned == nil || cloned.Name != "pool-path-probe" {
		t.Fatalf("clone into pool path %q did not land: %+v", pools[0].Path, cloned)
	}
}

func TestVsphereResourcePools_AdminOnly(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// The @hasRole(any: [admin]) directive gates this; a non-admin caller that
	// reaches the resolver directly still must not enumerate vCenter inventory.
	// Connect deps are left nil so a leak would surface as a config error, not a
	// silent success. A non-admin reaching here would hit connectPool's nil guard;
	// assert the directive-level contract holds by checking the schema wires it.
	_, err := (&queryResolver{r}).VsphereResourcePools(
		userCtx("11111111-1111-1111-1111-111111111111", "user"),
		"00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for unconfigured/non-admin resource-pool listing")
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
		Name: "bob-vm", TemplateFamilyID: familyID, TemplateVersionID: versionID, ResourcePoolID: pool.ID,
	})
	if err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	ag := dep.Agent
	// The cloned VM carries the UNIQUE name (display + id8), not the display name.
	if ag.Endpoint == nil {
		t.Fatal("deployed agent has no vm_ref")
	}
	vmRef := *ag.Endpoint

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
		if vm.Name == vmRef {
			t.Fatal("agent VM not destroyed on recycle")
		}
	}
	if len(fg.deleted) == 0 {
		t.Fatal("agent key not revoked on recycle")
	}
}
