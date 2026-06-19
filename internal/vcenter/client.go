// Package vcenter wraps govmomi for resource-pool access, inventory sync and
// agent-VM lifecycle. Replaces the legacy pyvmomi (vmware-skill) path. See LLD-03.
package vcenter

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Client is a thin govmomi wrapper scoped to the platform's needs.
type Client struct {
	vc *govmomi.Client
}

// Connect dials a vCenter and authenticates. insecure skips TLS verification
// (use only with a pinned internal CA in air-gapped environments).
func Connect(ctx context.Context, endpoint, user, pass string, insecure bool) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("vcenter: parse endpoint: %w", err)
	}
	u.User = url.UserPassword(user, pass)
	c, err := govmomi.NewClient(ctx, u, insecure)
	if err != nil {
		return nil, fmt.Errorf("vcenter: connect %s: %w", u.Host, err)
	}
	return &Client{vc: c}, nil
}

// VMInfo is a summarized virtual machine for inventory sync.
type VMInfo struct {
	Name       string
	PowerState string
	UUID       string
}

// ListVMs returns all virtual machines visible to the account.
func (c *Client) ListVMs(ctx context.Context) ([]VMInfo, error) {
	m := view.NewManager(c.vc.Client)
	v, err := m.CreateContainerView(ctx, c.vc.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return nil, fmt.Errorf("vcenter: create view: %w", err)
	}
	defer func() { _ = v.Destroy(ctx) }()

	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"summary"}, &vms); err != nil {
		return nil, fmt.Errorf("vcenter: retrieve vms: %w", err)
	}
	out := make([]VMInfo, 0, len(vms))
	for _, vm := range vms {
		out = append(out, VMInfo{
			Name:       vm.Summary.Config.Name,
			PowerState: string(vm.Summary.Runtime.PowerState),
			UUID:       vm.Summary.Config.Uuid,
		})
	}
	return out, nil
}

// SetGuestinfo writes guestinfo.* extraConfig onto a VM (base64-encoded), the
// per-VM config channel consumed by cloud-init at first boot (LLD-03 §2).
func (c *Client) SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	opts := make([]types.BaseOptionValue, 0, len(kv)*2)
	for k, val := range kv {
		enc := base64.StdEncoding.EncodeToString([]byte(val))
		opts = append(opts,
			&types.OptionValue{Key: "guestinfo." + k, Value: enc},
			&types.OptionValue{Key: "guestinfo." + k + ".encoding", Value: "base64"},
		)
	}
	task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{ExtraConfig: opts})
	if err != nil {
		return fmt.Errorf("vcenter: reconfigure %s: %w", vmName, err)
	}
	return task.Wait(ctx)
}

func (c *Client) findVM(ctx context.Context, name string) (*object.VirtualMachine, error) {
	m := view.NewManager(c.vc.Client)
	v, err := m.CreateContainerView(ctx, c.vc.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = v.Destroy(ctx) }()
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name"}, &vms); err != nil {
		return nil, err
	}
	for _, vm := range vms {
		if vm.Name == name {
			return object.NewVirtualMachine(c.vc.Client, vm.Reference()), nil
		}
	}
	return nil, fmt.Errorf("vcenter: vm %q not found", name)
}

// Logout terminates the session.
func (c *Client) Logout(ctx context.Context) error {
	return c.vc.Logout(ctx)
}
