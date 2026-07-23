package deploy

import (
	"context"
	"testing"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

type datastoreCaptureVC struct {
	lastClone vcenter.CloneSpec
}

func (f *datastoreCaptureVC) CloneFromTemplate(_ context.Context, spec vcenter.CloneSpec) (*vcenter.VMInfo, error) {
	f.lastClone = spec
	return &vcenter.VMInfo{Name: spec.Name, PowerState: "poweredOff"}, nil
}

func (f *datastoreCaptureVC) DeployOVF(context.Context, vcenter.OVFDeploySpec) (*vcenter.VMInfo, error) {
	return nil, nil
}

func (f *datastoreCaptureVC) SetGuestinfo(context.Context, string, map[string]string) error {
	return nil
}
func (f *datastoreCaptureVC) PowerOn(context.Context, string) error { return nil }
func (f *datastoreCaptureVC) Destroy(context.Context, string) error { return nil }

type noOpGateway struct{}

func (noOpGateway) GenerateKey(context.Context, gateway.GenerateKeyRequest) (*gateway.KeyResponse, error) {
	return nil, nil
}
func (noOpGateway) UpdateKey(context.Context, gateway.UpdateKeyRequest) error { return nil }
func (noOpGateway) RegenerateKey(context.Context, string) (*gateway.KeyResponse, error) {
	return nil, nil
}
func (noOpGateway) DeleteKey(context.Context, string) error { return nil }
func (noOpGateway) CreateTeam(context.Context, gateway.TeamRequest) (*gateway.TeamResponse, error) {
	return nil, nil
}
func (noOpGateway) DeleteTeam(context.Context, string) error { return nil }
func (noOpGateway) ListAvailableModels(context.Context) ([]string, error) {
	return nil, nil
}
func (noOpGateway) ListKeys(context.Context) ([]gateway.KeyInfo, error)   { return nil, nil }
func (noOpGateway) ListTeams(context.Context) ([]gateway.TeamInfo, error) { return nil, nil }

func TestProvisionSetsVsanDatastoreForClone(t *testing.T) {
	vc := &datastoreCaptureVC{}
	svc := &Service{
		Gateway:    noOpGateway{},
		VCenter:    vc,
		GatewayURL: "https://gw",
	}

	if _, err := svc.Provision(context.Background(), Request{
		UserID:           "u",
		Template:         "builder-opencode-temp-v7",
		VMName:           "agent-01",
		ExistingKey:      "sk-test",
		ExistingKeyToken: "tok-test",
	}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := vc.lastClone.Datastore; got != "vsanDatastore" {
		t.Fatalf("CloneSpec.Datastore = %q, want %q", got, "vsanDatastore")
	}
}
