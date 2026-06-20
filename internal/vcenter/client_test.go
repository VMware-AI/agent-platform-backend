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

func powerStateOf(t *testing.T, c *Client, name string) string {
	t.Helper()
	vms, err := c.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	for _, vm := range vms {
		if vm.Name == name {
			return vm.PowerState
		}
	}
	t.Fatalf("vm %q not found", name)
	return ""
}

func TestPowerCycle_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()
	vms, _ := c.ListVMs(ctx)
	target := vms[0].Name

	if err := c.PowerOff(ctx, target); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	if ps := powerStateOf(t, c, target); ps != "poweredOff" {
		t.Fatalf("after PowerOff state = %q", ps)
	}
	if err := c.PowerOn(ctx, target); err != nil {
		t.Fatalf("PowerOn: %v", err)
	}
	if ps := powerStateOf(t, c, target); ps != "poweredOn" {
		t.Fatalf("after PowerOn state = %q", ps)
	}
}

func TestShutdownGuest_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()
	vms, _ := c.ListVMs(ctx)
	target := vms[0].Name // poweredOn by default in vcsim

	if err := c.Shutdown(ctx, target); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if ps := powerStateOf(t, c, target); ps != "poweredOff" {
		t.Fatalf("after Shutdown state = %q", ps)
	}
}

func TestPowerOn_NotFound(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	if err := c.PowerOn(context.Background(), "no-such-vm"); err == nil {
		t.Fatal("expected error for missing vm")
	}
}

func TestCloneFromTemplate_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()
	vms, _ := c.ListVMs(ctx)
	src := vms[0].Name

	info, err := c.CloneFromTemplate(ctx, CloneSpec{
		Template: src, Name: "agent-clone-01", PowerOn: false,
	})
	if err != nil {
		t.Fatalf("CloneFromTemplate: %v", err)
	}
	if info.Name != "agent-clone-01" {
		t.Fatalf("clone name = %q", info.Name)
	}
	if info.PowerState != "poweredOff" {
		t.Fatalf("clone should be powered off (guestinfo-first), got %q", info.PowerState)
	}
	after, _ := c.ListVMs(ctx)
	found := false
	for _, vm := range after {
		if vm.Name == "agent-clone-01" {
			found = true
		}
	}
	if !found {
		t.Fatal("clone not present in inventory")
	}
}

func TestCloneFromTemplate_MissingArgs(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	if _, err := c.CloneFromTemplate(context.Background(), CloneSpec{Name: "x"}); err == nil {
		t.Fatal("expected error when template is empty")
	}
}

func TestDestroy_PoweredOff_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()
	vms, _ := c.ListVMs(ctx)
	if _, err := c.CloneFromTemplate(ctx, CloneSpec{Template: vms[0].Name, Name: "doomed-off"}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := c.Destroy(ctx, "doomed-off"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	for _, vm := range mustListVMs(t, c) {
		if vm.Name == "doomed-off" {
			t.Fatal("vm still present after Destroy")
		}
	}
}

func TestDestroy_PoweredOn_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()
	vms, _ := c.ListVMs(ctx)
	target := vms[0].Name // poweredOn default — exercises power-off-first
	if err := c.Destroy(ctx, target); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	for _, vm := range mustListVMs(t, c) {
		if vm.Name == target {
			t.Fatal("vm still present after Destroy")
		}
	}
}

func TestListTemplates_VCSim(t *testing.T) {
	c, cleanup := withSim(t)
	defer cleanup()
	ctx := context.Background()

	tpls, err := c.ListTemplates(ctx)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(tpls) != 0 {
		t.Fatalf("expected 0 templates initially, got %d", len(tpls))
	}

	// mark a VM as a template (must be powered off first)
	target := mustListVMs(t, c)[0].Name
	if err := c.PowerOff(ctx, target); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	if err := c.MarkAsTemplate(ctx, target); err != nil {
		t.Fatalf("MarkAsTemplate: %v", err)
	}

	tpls, err = c.ListTemplates(ctx)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(tpls) != 1 || tpls[0].Name != target {
		t.Fatalf("expected 1 template %q, got %+v", target, tpls)
	}
}

func mustListVMs(t *testing.T, c *Client) []VMInfo {
	t.Helper()
	vms, err := c.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	return vms
}

func TestConnect_BadEndpoint(t *testing.T) {
	_, err := Connect(context.Background(), "://bad", "u", "p", true)
	if err == nil {
		t.Fatal("expected error on malformed endpoint")
	}
}
