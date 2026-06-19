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
func (f *fakeGateway) DeleteKey(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return &gateway.TeamResponse{}, nil
}

// failingSetter always fails guestinfo injection, to exercise orphan cleanup.
type failingSetter struct{}

func (failingSetter) SetGuestinfo(context.Context, string, map[string]string) error {
	return context.Canceled
}

func TestProvision_RevokesKeyOnGuestinfoFailure(t *testing.T) {
	fg := &fakeGateway{}
	svc := &Service{Gateway: fg, VCenter: failingSetter{}, GatewayURL: "https://gw"}
	if _, err := svc.Provision(context.Background(), Request{UserID: "u", VMName: "vm1"}); err == nil {
		t.Fatal("expected provision error on guestinfo failure")
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "sk-deploy-xyz" {
		t.Fatalf("issued key should be revoked on failure: %+v", fg.deleted)
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
	target := vms[0].Name

	fg := &fakeGateway{}
	svc := &Service{Gateway: fg, VCenter: vc, GatewayURL: "https://gateway.internal/"}

	budget := 50.0
	res, err := svc.Provision(ctx, Request{
		AgentName: "alice-goose", UserID: "u-alice", VMName: target,
		Hostname: "agent-vm-01", MaxBudget: &budget,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
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
