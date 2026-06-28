// Package vcenter wraps govmomi for resource-pool access, inventory sync and
// agent-VM lifecycle. Replaces the legacy pyvmomi (vmware-skill) path. See LLD-03.
package vcenter

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Client is a thin govmomi wrapper scoped to the platform's needs.
type Client struct {
	vc *govmomi.Client
	// userinfo is kept so the vAPI (REST) endpoints — content library, etc. —
	// can establish their own session, distinct from the SOAP one (govmomi's
	// REST and SOAP clients each log in separately). See library.go.
	userinfo *url.Userinfo
}

// normalizeEndpoint ensures the endpoint targets the vCenter SOAP SDK path.
// Operators commonly enter just the host (e.g. https://vc.example.local), but
// govmomi requires the /sdk endpoint — without it the SOAP POST hits "/" and
// vCenter answers 400. We append /sdk only when no meaningful path is present;
// an explicit path (an existing /sdk, or a reverse-proxy prefix) is left as-is.
func normalizeEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint // let Connect surface the parse error
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/sdk"
	}
	return u.String()
}

// Connect dials a vCenter and authenticates. insecure skips TLS verification
// (use only with a pinned internal CA in air-gapped environments). The endpoint
// is normalized to the /sdk path when the caller omits it (see normalizeEndpoint).
func Connect(ctx context.Context, endpoint, user, pass string, insecure bool) (*Client, error) {
	u, err := url.Parse(normalizeEndpoint(endpoint))
	if err != nil {
		return nil, fmt.Errorf("vcenter: parse endpoint: %w", err)
	}
	u.User = url.UserPassword(user, pass)
	c, err := govmomi.NewClient(ctx, u, insecure)
	if err != nil {
		return nil, fmt.Errorf("vcenter: connect %s: %w", u.Host, err)
	}
	return &Client{vc: c, userinfo: u.User}, nil
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

// CloneSpec describes a clone-from-template provisioning request. Placement
// fields are optional — empty values fall back to the datacenter defaults.
type CloneSpec struct {
	Template     string // source template (or VM) name
	Name         string // new VM name
	PowerOn      bool   // power on immediately (leave false to inject guestinfo first)
	ResourcePool string // target pool; "" = datacenter default
	Datastore    string // target datastore; "" = inherit from source
	Folder       string // target VM folder; "" = datacenter VM folder
}

// CloneFromTemplate clones a template VM into a new agent VM and returns its
// info. This is the auto-provision primitive: the deploy flow clones (powered
// off), injects guestinfo, then powers on so cloud-init consumes it at first
// boot (LLD-03 §4 部署).
func (c *Client) CloneFromTemplate(ctx context.Context, spec CloneSpec) (*VMInfo, error) {
	if spec.Template == "" || spec.Name == "" {
		return nil, fmt.Errorf("vcenter: clone requires template and name")
	}
	src, err := c.findVM(ctx, spec.Template)
	if err != nil {
		return nil, err
	}
	finder := find.NewFinder(c.vc.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: default datacenter: %w", err)
	}
	finder.SetDatacenter(dc)

	relocate := types.VirtualMachineRelocateSpec{}
	if spec.ResourcePool != "" {
		pool, err := finder.ResourcePool(ctx, spec.ResourcePool)
		if err != nil {
			return nil, fmt.Errorf("vcenter: resource pool %q: %w", spec.ResourcePool, err)
		}
		relocate.Pool = types.NewReference(pool.Reference())
	} else {
		// Default: place the clone in the same pool as the source. A true
		// template has no pool, so the caller must then specify one.
		var msrc mo.VirtualMachine
		if err := src.Properties(ctx, src.Reference(), []string{"resourcePool"}, &msrc); err != nil {
			return nil, fmt.Errorf("vcenter: read source pool: %w", err)
		}
		if msrc.ResourcePool == nil {
			return nil, fmt.Errorf("vcenter: source %q has no resource pool; specify resourcePool", spec.Template)
		}
		relocate.Pool = msrc.ResourcePool
	}
	if spec.Datastore != "" {
		ds, err := finder.Datastore(ctx, spec.Datastore)
		if err != nil {
			return nil, fmt.Errorf("vcenter: datastore %q: %w", spec.Datastore, err)
		}
		relocate.Datastore = types.NewReference(ds.Reference())
	}

	folder, err := c.cloneFolder(ctx, finder, dc, spec.Folder)
	if err != nil {
		return nil, err
	}
	task, err := src.Clone(ctx, folder, spec.Name, types.VirtualMachineCloneSpec{
		Location: relocate,
		PowerOn:  spec.PowerOn,
		Template: false,
	})
	if err != nil {
		return nil, fmt.Errorf("vcenter: clone %s→%s: %w", spec.Template, spec.Name, err)
	}
	if err := task.Wait(ctx); err != nil {
		return nil, fmt.Errorf("vcenter: clone task %s→%s: %w", spec.Template, spec.Name, err)
	}
	return c.vmInfo(ctx, spec.Name)
}

// cloneFolder resolves the target VM folder, defaulting to the datacenter's.
func (c *Client) cloneFolder(ctx context.Context, finder *find.Finder, dc *object.Datacenter, name string) (*object.Folder, error) {
	if name != "" {
		f, err := finder.Folder(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("vcenter: folder %q: %w", name, err)
		}
		return f, nil
	}
	folders, err := dc.Folders(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: datacenter folders: %w", err)
	}
	return folders.VmFolder, nil
}

// vmInfo returns the summarized info for a single VM by name.
func (c *Client) vmInfo(ctx context.Context, name string) (*VMInfo, error) {
	vm, err := c.findVM(ctx, name)
	if err != nil {
		return nil, err
	}
	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"summary"}, &mvm); err != nil {
		return nil, fmt.Errorf("vcenter: vm properties %s: %w", name, err)
	}
	return &VMInfo{
		Name:       mvm.Summary.Config.Name,
		PowerState: string(mvm.Summary.Runtime.PowerState),
		UUID:       mvm.Summary.Config.Uuid,
	}, nil
}

// PowerOn powers a VM on and waits for completion (LLD-03 §4 开关机).
func (c *Client) PowerOn(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	task, err := vm.PowerOn(ctx)
	if err != nil {
		return fmt.Errorf("vcenter: power on %s: %w", vmName, err)
	}
	return task.Wait(ctx)
}

// PowerOff hard-powers a VM off and waits for completion. Prefer Shutdown for a
// graceful guest stop; PowerOff is the forceful fallback (LLD-03 §4).
func (c *Client) PowerOff(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	task, err := vm.PowerOff(ctx)
	if err != nil {
		return fmt.Errorf("vcenter: power off %s: %w", vmName, err)
	}
	return task.Wait(ctx)
}

// Shutdown requests a graceful guest shutdown via VMware Tools. Unlike PowerOff
// it is not a task — the guest powers off asynchronously (LLD-03 §4).
func (c *Client) Shutdown(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	if err := vm.ShutdownGuest(ctx); err != nil {
		return fmt.Errorf("vcenter: shutdown guest %s: %w", vmName, err)
	}
	return nil
}

// ListTemplates returns VMs marked as templates (config.template=true) — the
// OVA-built images available to clone agent VMs from (LLD-03 §4, 部署表单选模板).
func (c *Client) ListTemplates(ctx context.Context) ([]VMInfo, error) {
	m := view.NewManager(c.vc.Client)
	v, err := m.CreateContainerView(ctx, c.vc.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return nil, fmt.Errorf("vcenter: create view: %w", err)
	}
	defer func() { _ = v.Destroy(ctx) }()
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"summary"}, &vms); err != nil {
		return nil, fmt.Errorf("vcenter: retrieve templates: %w", err)
	}
	out := make([]VMInfo, 0)
	for _, vm := range vms {
		if vm.Summary.Config.Template {
			out = append(out, VMInfo{
				Name:       vm.Summary.Config.Name,
				PowerState: string(vm.Summary.Runtime.PowerState),
				UUID:       vm.Summary.Config.Uuid,
			})
		}
	}
	return out, nil
}

// ResourcePoolInfo is a placement resource pool offered to the deploy form.
// Path is the inventory path (e.g. "/DC0/host/DC0_C0/Resources") that
// CloneFromTemplate's finder.ResourcePool resolves — full path so it stays
// unambiguous across multiple datacenters. Name is the human label.
type ResourcePoolInfo struct {
	Name string
	Path string
}

// ListResourcePools enumerates every resource pool visible to the account, as
// placement targets for a clone (LLD-03 §4 部署: a true OVA template has no
// source pool, so a deploy must pick a placement pool). Mirrors ListTemplates:
// it dials the same vCenter and returns name + inventory path. The path is the
// value CloneFromTemplate's CloneSpec.ResourcePool expects.
//
// Pools are enumerated per-datacenter and returned with their FULL inventory
// path (e.g. "/DC0/host/DC0_C0/Resources"), so two datacenters that both have a
// "Resources" pool stay distinct and the path round-trips through placement.
func (c *Client) ListResourcePools(ctx context.Context) ([]ResourcePoolInfo, error) {
	finder := find.NewFinder(c.vc.Client, true)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil {
		if _, ok := err.(*find.NotFoundError); ok {
			return []ResourcePoolInfo{}, nil
		}
		return nil, fmt.Errorf("vcenter: list datacenters: %w", err)
	}
	out := make([]ResourcePoolInfo, 0)
	for _, dc := range dcs {
		// Scope the finder to this datacenter so the wildcard pool search is
		// unambiguous (a root-level "*" errors with "please specify a datacenter"
		// when more than one exists).
		dcFinder := find.NewFinder(c.vc.Client, true).SetDatacenter(dc)
		pools, err := dcFinder.ResourcePoolList(ctx, "*")
		if err != nil {
			if _, ok := err.(*find.NotFoundError); ok {
				continue // datacenter with no compute/pools — skip, not an error
			}
			return nil, fmt.Errorf("vcenter: list resource pools in %q: %w", dc.InventoryPath, err)
		}
		for _, p := range pools {
			out = append(out, ResourcePoolInfo{Name: p.Name(), Path: p.InventoryPath})
		}
	}
	return out, nil
}

// MarkAsTemplate converts a powered-off VM into a template, making it available
// to clone agents from (LLD-03 §4; the OVA-build → template step).
func (c *Client) MarkAsTemplate(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	if err := vm.MarkAsTemplate(ctx); err != nil {
		return fmt.Errorf("vcenter: mark template %s: %w", vmName, err)
	}
	return nil
}

// Destroy powers a VM off (if needed) and permanently deletes it. This is
// destructive — the resolver layer gates it with dry-run + confirmation + audit
// (LLD-03 §4 回收, 沿用旧平台 @vmware_tool 约束精神).
func (c *Client) Destroy(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"runtime"}, &mvm); err != nil {
		return fmt.Errorf("vcenter: read power state %s: %w", vmName, err)
	}
	if mvm.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		task, err := vm.PowerOff(ctx)
		if err != nil {
			return fmt.Errorf("vcenter: power off before destroy %s: %w", vmName, err)
		}
		if err := task.Wait(ctx); err != nil {
			return fmt.Errorf("vcenter: power off task %s: %w", vmName, err)
		}
	}
	task, err := vm.Destroy(ctx)
	if err != nil {
		return fmt.Errorf("vcenter: destroy %s: %w", vmName, err)
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
	var match *object.VirtualMachine
	for _, vm := range vms {
		if vm.Name == name {
			if match != nil {
				// Display names are mutable and not guaranteed unique in vCenter. A
				// destructive op (power/destroy) must never guess which duplicate to
				// hit — fail loudly instead of silently taking the first match.
				return nil, fmt.Errorf("vcenter: multiple VMs named %q; refusing op on an ambiguous name", name)
			}
			match = object.NewVirtualMachine(c.vc.Client, vm.Reference())
		}
	}
	if match == nil {
		return nil, fmt.Errorf("vcenter: vm %q not found", name)
	}
	return match, nil
}

// Inventory counts the resource pool's datacenters/clusters/hosts/VMs
// (0619 第13页 资源池接入列).
type Inventory struct {
	Datacenters int
	Clusters    int
	Hosts       int
	VMs         int
}

// Inventory returns entity counts via container views.
func (c *Client) Inventory(ctx context.Context) (Inventory, error) {
	m := view.NewManager(c.vc.Client)
	root := c.vc.ServiceContent.RootFolder

	count := func(kind string, dest any) (int, error) {
		v, err := m.CreateContainerView(ctx, root, []string{kind}, true)
		if err != nil {
			return 0, err
		}
		defer func() { _ = v.Destroy(ctx) }()
		if err := v.Retrieve(ctx, []string{kind}, []string{"name"}, dest); err != nil {
			return 0, err
		}
		switch d := dest.(type) {
		case *[]mo.Datacenter:
			return len(*d), nil
		case *[]mo.ClusterComputeResource:
			return len(*d), nil
		case *[]mo.HostSystem:
			return len(*d), nil
		case *[]mo.VirtualMachine:
			return len(*d), nil
		}
		return 0, nil
	}

	var inv Inventory
	var err error
	if inv.Datacenters, err = count("Datacenter", &[]mo.Datacenter{}); err != nil {
		return inv, err
	}
	if inv.Clusters, err = count("ClusterComputeResource", &[]mo.ClusterComputeResource{}); err != nil {
		return inv, err
	}
	if inv.Hosts, err = count("HostSystem", &[]mo.HostSystem{}); err != nil {
		return inv, err
	}
	if inv.VMs, err = count("VirtualMachine", &[]mo.VirtualMachine{}); err != nil {
		return inv, err
	}
	return inv, nil
}

// AboutInfo summarizes the vCenter server identity, available without an extra
// round-trip (govmomi populates ServiceContent.About at connect time).
type AboutInfo struct {
	Version  string // e.g. "8.0.3.00800"
	Build    string // e.g. "25197330"
	FullName string // e.g. "VMware vCenter Server 8.0.3 build-25197330"
}

// About returns the connected vCenter's version/build identity.
func (c *Client) About() AboutInfo {
	a := c.vc.ServiceContent.About
	return AboutInfo{Version: a.Version, Build: a.Build, FullName: a.FullName}
}

// Logout terminates the session.
func (c *Client) Logout(ctx context.Context) error {
	return c.vc.Logout(ctx)
}
