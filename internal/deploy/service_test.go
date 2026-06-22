package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

type fakeGateway struct {
	lastReq gateway.GenerateKeyRequest
	deleted []string
}

func (f *fakeGateway) GenerateKey(_ context.Context, req gateway.GenerateKeyRequest) (*gateway.KeyResponse, error) {
	f.lastReq = req
	return &gateway.KeyResponse{Key: "sk-deploy-xyz", UserID: req.UserID}, nil
}
func (f *fakeGateway) UpdateKey(context.Context, gateway.UpdateKeyRequest) error { return nil }
func (f *fakeGateway) RegenerateKey(context.Context, string) (*gateway.KeyResponse, error) {
	return &gateway.KeyResponse{}, nil
}
func (f *fakeGateway) DeleteKey(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return &gateway.TeamResponse{}, nil
}
func (f *fakeGateway) DeleteTeam(context.Context, string) error { return nil }
func (f *fakeGateway) ListKeys(context.Context) ([]gateway.KeyInfo, error) {
	return nil, nil
}
func (f *fakeGateway) ListTeams(context.Context) ([]gateway.TeamInfo, error) {
	return nil, nil
}

// fakeVC is an in-memory VMProvisioner; failAt forces a chosen step to fail.
type fakeVC struct {
	cloned    []string
	guestinfo []string
	lastGI    map[string]string // last guestinfo kv injected
	poweredOn []string
	destroyed []string
	failAt    string // "", "clone", "guestinfo", "poweron"
}

func (f *fakeVC) CloneFromTemplate(_ context.Context, spec vcenter.CloneSpec) (*vcenter.VMInfo, error) {
	if f.failAt == "clone" {
		return nil, context.Canceled
	}
	f.cloned = append(f.cloned, spec.Name)
	return &vcenter.VMInfo{Name: spec.Name, PowerState: "poweredOff"}, nil
}
func (f *fakeVC) SetGuestinfo(_ context.Context, vm string, kv map[string]string) error {
	if f.failAt == "guestinfo" {
		return context.Canceled
	}
	f.guestinfo = append(f.guestinfo, vm)
	f.lastGI = kv
	return nil
}
func (f *fakeVC) PowerOn(_ context.Context, vm string) error {
	if f.failAt == "poweron" {
		return context.Canceled
	}
	f.poweredOn = append(f.poweredOn, vm)
	return nil
}
func (f *fakeVC) Destroy(_ context.Context, vm string) error {
	f.destroyed = append(f.destroyed, vm)
	return nil
}

func TestProvision_FullLifecycle(t *testing.T) {
	fg := &fakeGateway{}
	vc := &fakeVC{}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gw"}
	res, err := svc.Provision(context.Background(), Request{
		UserID: "u", Template: "ova-ubuntu-agent", VMName: "agent-01", Hostname: "agent-01",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(vc.cloned) != 1 || vc.cloned[0] != "agent-01" {
		t.Fatalf("clone not called: %+v", vc.cloned)
	}
	if len(vc.guestinfo) != 1 || len(vc.poweredOn) != 1 {
		t.Fatalf("guestinfo/poweron not called: gi=%+v on=%+v", vc.guestinfo, vc.poweredOn)
	}
	if len(vc.destroyed) != 0 {
		t.Fatalf("nothing should be destroyed on success: %+v", vc.destroyed)
	}
	if res.VMName != "agent-01" || res.VirtualKey != "sk-deploy-xyz" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// LLD-11 K2: knowledge pack ids ride in guestinfo alongside the agent-manager
// channel, so the daemon can pull each bundle over the §6 control-plane endpoint.
func TestProvision_InjectsKnowledgePacks(t *testing.T) {
	fg := &fakeGateway{}
	vc := &fakeVC{}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gw"}
	if _, err := svc.Provision(context.Background(), Request{
		UserID: "u", Template: "ova", VMName: "vm1",
		VMID: "vm1", EnrollToken: "enr-tok", ControlPlaneURL: "https://cp",
		KnowledgePackIDs: []string{"id-a", "id-b"},
	}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := vc.lastGI["agentmgr.knowledge_packs"]; got != "id-a,id-b" {
		t.Fatalf("knowledge_packs guestinfo = %q, want \"id-a,id-b\"", got)
	}

	// Without the agent-manager channel there is no way to pull, so no packs key.
	vc2 := &fakeVC{}
	svc2 := &Service{Gateway: fg, VCenter: vc2, GatewayURL: "https://gw"}
	if _, err := svc2.Provision(context.Background(), Request{
		UserID: "u", Template: "ova", VMName: "vm2",
		KnowledgePackIDs: []string{"id-a"},
	}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if _, ok := vc2.lastGI["agentmgr.knowledge_packs"]; ok {
		t.Fatal("knowledge_packs should be absent without the agent-manager channel")
	}
}

func TestProvision_RollbackOnCloneFailure(t *testing.T) {
	fg := &fakeGateway{}
	vc := &fakeVC{failAt: "clone"}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gw"}
	if _, err := svc.Provision(context.Background(), Request{UserID: "u", Template: "ova", VMName: "vm1"}); err == nil {
		t.Fatal("expected clone failure")
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-deploy-xyz" {
		t.Fatalf("key not revoked on clone failure: %+v", fg.deleted)
	}
	if len(vc.destroyed) != 0 {
		t.Fatalf("no VM should be destroyed (clone failed): %+v", vc.destroyed)
	}
}

func TestProvision_RollbackOnGuestinfoFailure(t *testing.T) {
	fg := &fakeGateway{}
	vc := &fakeVC{failAt: "guestinfo"}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gw"}
	if _, err := svc.Provision(context.Background(), Request{UserID: "u", Template: "ova", VMName: "vm1"}); err == nil {
		t.Fatal("expected guestinfo failure")
	}
	if len(vc.destroyed) != 1 || vc.destroyed[0] != "vm1" {
		t.Fatalf("cloned VM not destroyed: %+v", vc.destroyed)
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-deploy-xyz" {
		t.Fatalf("key not revoked: %+v", fg.deleted)
	}
}

func TestProvision_RollbackOnPowerOnFailure(t *testing.T) {
	fg := &fakeGateway{}
	vc := &fakeVC{failAt: "poweron"}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gw"}
	if _, err := svc.Provision(context.Background(), Request{UserID: "u", Template: "ova", VMName: "vm1"}); err == nil {
		t.Fatal("expected power-on failure")
	}
	if len(vc.destroyed) != 1 || len(fg.deleted) != 1 {
		t.Fatalf("VM+key not rolled back: destroyed=%+v deleted=%+v", vc.destroyed, fg.deleted)
	}
}

func TestProvision_WiresGatewayAndVCenter(t *testing.T) {
	// real vCenter client against vcsim
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatalf("vcsim: %v", err)
	}
	srv := model.Service.NewServer()
	defer srv.Close()
	defer model.Remove()

	ctx := context.Background()
	vc, err := vcenter.Connect(ctx, srv.URL.String(), "u", "p", true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer vc.Logout(ctx)

	vms, err := vc.ListVMs(ctx)
	if err != nil || len(vms) == 0 {
		t.Fatalf("list vms: %v (n=%d)", err, len(vms))
	}
	template := vms[0].Name // clone source (stands in for the OVA template)

	fg := &fakeGateway{}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gateway.internal/"}

	budget := 50.0
	res, err := svc.Provision(ctx, Request{
		AgentName: "alice-goose", UserID: "u-alice",
		Template: template, VMName: "agent-alice-01",
		Hostname: "agent-vm-01", MaxBudget: &budget,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if res.VMName != "agent-alice-01" {
		t.Fatalf("created VM name = %q", res.VMName)
	}

	// the cloned VM exists and is powered on (cloud-init consumes guestinfo at boot)
	after, _ := vc.ListVMs(ctx)
	var found *vcenter.VMInfo
	for i := range after {
		if after[i].Name == "agent-alice-01" {
			found = &after[i]
		}
	}
	if found == nil {
		t.Fatal("cloned VM not in inventory")
	}
	if found.PowerState != "poweredOn" {
		t.Fatalf("cloned VM should be powered on, got %q", found.PowerState)
	}

	// gateway issued the key with defaults (smart router)
	if res.VirtualKey != "sk-deploy-xyz" {
		t.Fatalf("virtual key = %q", res.VirtualKey)
	}
	if len(fg.lastReq.Models) != 1 || fg.lastReq.Models[0] != "smart" {
		t.Fatalf("default model should be 'smart': %+v", fg.lastReq.Models)
	}
	if fg.lastReq.UserID != "u-alice" {
		t.Fatalf("gateway userId = %q", fg.lastReq.UserID)
	}

	// userdata carries the gateway URL + the issued key
	if !strings.Contains(res.Userdata, "OPENAI_BASE_URL=https://gateway.internal/v1") {
		t.Fatalf("userdata missing gateway url:\n%s", res.Userdata)
	}
	if !strings.Contains(res.Userdata, "OPENAI_API_KEY=sk-deploy-xyz") {
		t.Fatalf("userdata missing key:\n%s", res.Userdata)
	}
	if !strings.Contains(res.Userdata, "hostname: agent-vm-01") {
		t.Fatalf("userdata missing hostname:\n%s", res.Userdata)
	}
}

func TestProvision_Validation(t *testing.T) {
	svc := &Service{Gateway: &fakeGateway{}, VCenter: nil}
	if _, err := svc.Provision(context.Background(), Request{VMName: "x", UserID: "u"}); err == nil {
		t.Fatal("nil vcenter should error")
	}
}

func TestProvision_EmbedsInlineDefaultConfig(t *testing.T) {
	svc := &Service{Gateway: &fakeGateway{}, VCenter: &fakeVC{}, GatewayURL: "https://gw.internal"}
	res, err := svc.Provision(context.Background(), Request{
		UserID: "u1", Template: "tpl", VMName: "vm1",
		DefaultConfig: "model: smart\nlog: debug", ConfigPath: "/etc/agent/config.yaml",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !strings.Contains(res.Userdata, "path: /etc/agent/config.yaml") {
		t.Fatalf("config path not embedded:\n%s", res.Userdata)
	}
	if !strings.Contains(res.Userdata, "model: smart") || !strings.Contains(res.Userdata, "log: debug") {
		t.Fatalf("config content not embedded:\n%s", res.Userdata)
	}
	// gateway env must still be present
	if !strings.Contains(res.Userdata, "OPENAI_API_KEY=") {
		t.Fatalf("gateway env missing:\n%s", res.Userdata)
	}
}

func TestProvision_NoConfig_GatewayEnvOnly(t *testing.T) {
	svc := &Service{Gateway: &fakeGateway{}, VCenter: &fakeVC{}, GatewayURL: "https://gw.internal"}
	res, err := svc.Provision(context.Background(), Request{UserID: "u1", Template: "t", VMName: "vm"})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if strings.Contains(res.Userdata, "/etc/agent/config") {
		t.Fatalf("no config should be injected when none provided:\n%s", res.Userdata)
	}
}
