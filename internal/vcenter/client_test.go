package vcenter

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
)

// withSim spins an in-memory vCenter (vcsim) and returns a connected Client.
func withSim(t *testing.T) (*Client, func()) {
	t.Helper()
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatalf("vcsim model create: %v", err)
	}
	srv := model.Service.NewServer()
	c, err := Connect(context.Background(), srv.URL.String(), "user", "pass", true)
	if err != nil {
		srv.Close()
		model.Remove()
		t.Fatalf("connect: %v", err)
	}
	return c, func() {
		_ = c.Logout(context.Background())
		srv.Close()
		model.Remove()
	}
}

func TestListVMs_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()

	vms, err := c.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	if len(vms) == 0 {
		t.Fatal("vcsim default inventory should contain VMs")
	}
	for _, vm := range vms {
		if vm.Name == "" || vm.PowerState == "" {
			t.Fatalf("VMInfo not populated: %+v", vm)
		}
	}
}

func TestSetGuestinfo_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()

	vms, err := c.ListVMs(ctx)
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	target := vms[0].Name

	err = c.SetGuestinfo(ctx, target, map[string]string{
		"userdata": "#cloud-config\nhostname: agent-vm-01\n",
	})
	if err != nil {
		t.Fatalf("SetGuestinfo: %v", err)
	}
}

func TestConnect_BadEndpoint(t *testing.T) {
	_, err := Connect(context.Background(), "://bad", "u", "p", true)
	if err == nil {
		t.Fatal("expected error on malformed endpoint")
	}
}
