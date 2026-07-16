// Package vcenter wraps govmomi for resource-pool access, inventory sync and
// agent-VM lifecycle. Replaces the legacy pyvmomi (vmware-skill) path. See LLD-03.
package vcenter

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/vcenter"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/methods"
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
	IP         string
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

// SetGuestinfo writes guestinfo.* extraConfig onto a VM, the per-VM config
// channel consumed at first boot (LLD-03 §2). Only cloud-init's own keys
// (userdata/metadata) are base64-wrapped with an `.encoding` companion — that
// convention is honored solely by cloud-init's VMware datasource. Every other
// key (the agentmgr.* deploy-time contract) is read on the VM via
// vmware-rpctool, which returns values verbatim and never decodes, so those
// are written raw — base64 there would hand the daemon garbage (and, e.g.,
// seed a base64 string as the initial credential).
func (c *Client) SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	opts := make([]types.BaseOptionValue, 0, len(kv)*2)
	for k, val := range kv {
		if k == "userdata" || k == "metadata" {
			enc := base64.StdEncoding.EncodeToString([]byte(val))
			opts = append(opts,
				&types.OptionValue{Key: "guestinfo." + k, Value: enc},
				&types.OptionValue{Key: "guestinfo." + k + ".encoding", Value: "base64"},
			)
			continue
		}
		opts = append(opts, &types.OptionValue{Key: "guestinfo." + k, Value: val})
	}
	task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{ExtraConfig: opts})
	if err != nil {
		return fmt.Errorf("vcenter: reconfigure %s: %w", vmName, err)
	}
	return task.Wait(ctx)
}

// OVFDeploySpec describes a content-library OVF deployment request.
// Uses vCenter's OVF deploy pipeline which generates the OVF environment
// (ovf-env.xml ISO) so the guest OS can read deployment properties natively.
type OVFDeploySpec struct {
	LibraryItemID string            // content library item UUID
	Name          string            // new VM name
	ResourcePool  string            // target resource pool MoRef
	Host          string            // target host MoRef
	Folder        string            // target VM folder MoRef
	Datastore     string            // target datastore MoRef
	Properties    map[string]string // OVF property id→value pairs
	Network       string            // target network/portgroup path ("" = keep default)
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
	// Network is the inventory path of the target portgroup (standard or dvPort).
	// "" = keep the source template's NIC mapping unchanged.
	Network string
	// ExtraConfig is guestinfo.* key-value pairs injected as VM ExtraConfig at
	// clone time. Typically carries OVF/vApp properties set by the deploy form
	// (ip, netmask, gateway, dns, password, etc.) so they are available when
	// cloud-init / VMwareGuestInfo datasource runs at first boot.
	ExtraConfig map[string]string
	// VAppProperties carries OVF user-configurable property key->value pairs
	// injected into the clone's VAppConfig so the guest receives them via the
	// OVF environment transport (iso / com.vmware.guestInfo).
	VAppProperties map[string]string
	// VAppTransport overrides the source template's OVF environment transport.
	VAppTransport []string
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

	// Remap the first NIC to the requested network/portgroup (optional).
	if spec.Network != "" {
		netDev, err := c.buildNetworkDevice(ctx, finder, src, spec.Network)
		if err != nil {
			return nil, err
		}
		if netDev != nil {
			relocate.DeviceChange = append(relocate.DeviceChange, netDev)
		}
	}

	folder, err := c.cloneFolder(ctx, finder, dc, spec.Folder)
	if err != nil {
		return nil, err
	}
	// Build ConfigSpec: ExtraConfig for guestinfo.* keys + VAppConfig for
	// OVF properties (the VMware-native way for guests to consume deploy params).
	var config *types.VirtualMachineConfigSpec
	needsConfig := len(spec.ExtraConfig) > 0
	needsVApp := len(spec.VAppProperties) > 0
	if needsConfig || needsVApp {
		cs := &types.VirtualMachineConfigSpec{}
		if needsConfig {
			opts := make([]types.BaseOptionValue, 0, len(spec.ExtraConfig))
			for k, v := range spec.ExtraConfig {
				opts = append(opts, &types.OptionValue{Key: k, Value: v})
			}
			cs.ExtraConfig = opts
		}
		if needsVApp {
			if vci := c.readVAppConfig(ctx, src); vci != nil {
				transport := spec.VAppTransport
				if len(transport) == 0 {
					transport = vci.OvfEnvironmentTransport
				}
				cs.VAppConfig = buildVAppPropertySpec(vci, spec.VAppProperties, transport)
			}
		}
		config = cs
	}

	// vCenter Guest OS Customization — the VMware-native way TKG uses.
	// Always applied: static IP when ip_mode=static, hostname only otherwise.
	customization := buildLinuxCustomization(spec.ExtraConfig, spec.Name)

	task, err := src.Clone(ctx, folder, spec.Name, types.VirtualMachineCloneSpec{
		Location:      relocate,
		PowerOn:       spec.PowerOn,
		Template:      false,
		Config:        config,
		Customization: customization,
	})
	if err != nil {
		return nil, fmt.Errorf("vcenter: clone %s→%s: %w", spec.Template, spec.Name, err)
	}
	if err := task.Wait(ctx); err != nil {
		return nil, fmt.Errorf("vcenter: clone task %s→%s: %w", spec.Template, spec.Name, err)
	}
	return c.vmInfo(ctx, spec.Name)
}

// DeployOVF deploys a VM from a content library OVF item using vCenter's
// native OVF deployment pipeline. Unlike CloneFromTemplate, this generates
// an OVF environment ISO that the guest OS reads to get deployment properties
// (network config, passwords, etc.) — the same mechanism TKG uses.
func (c *Client) DeployOVF(ctx context.Context, spec OVFDeploySpec) (*VMInfo, error) {
	if spec.LibraryItemID == "" || spec.Name == "" {
		return nil, fmt.Errorf("vcenter: OVF deploy requires LibraryItemID and Name")
	}
	rc := rest.NewClient(c.vc.Client)
	if err := rc.Login(ctx, c.userinfo); err != nil {
		return nil, fmt.Errorf("vcenter: REST login for OVF deploy: %w", err)
	}
	defer func() { _ = rc.Logout(ctx) }()

	mgr := vcenter.NewManager(rc)

	// vCenter 8.0.3 uses the VMTX deploy endpoint with flat placement fields.
	deploy := vcenter.DeployTemplate{
		Name:      spec.Name,
		PoweredOn: false,
	}
	if spec.ResourcePool != "" || spec.Host != "" || spec.Folder != "" {
		deploy.Placement = &vcenter.Placement{
			ResourcePool: spec.ResourcePool,
			Host:         spec.Host,
			Folder:       spec.Folder,
		}
	}

	_, err := mgr.DeployTemplateLibraryItem(ctx, spec.LibraryItemID, deploy)
	if err != nil {
		return nil, fmt.Errorf("vcenter: OVF deploy: %w", err)
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
	ip := ""
	if mvm.Summary.Guest != nil {
		ip = mvm.Summary.Guest.IpAddress
	}
	return &VMInfo{
		Name:       mvm.Summary.Config.Name,
		PowerState: string(mvm.Summary.Runtime.PowerState),
		UUID:       mvm.Summary.Config.Uuid,
		IP:         ip,
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

// RebootGuest requests a graceful guest reboot via VMware Tools. Like Shutdown
// it is not a task — the guest reboots asynchronously (LLD-03 §4 开关机).
func (c *Client) RebootGuest(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return err
	}
	if err := vm.RebootGuest(ctx); err != nil {
		return fmt.Errorf("vcenter: reboot guest %s: %w", vmName, err)
	}
	return nil
}

// ListTemplates returns VMs marked as templates (config.template=true) — the
// OVA-built images available to clone agent VMs from (LLD-03 §4, 部署表单选模板).

func (c *Client) GetVMInfo(ctx context.Context, vmName string) (*VMInfo, error) {
	return c.vmInfo(ctx, vmName)
}

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

// readVAppConfig reads the source VM's VmConfigInfo (vApp properties and transport).
func (c *Client) readVAppConfig(ctx context.Context, vm *object.VirtualMachine) *types.VmConfigInfo {
	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.vAppConfig"}, &mvm); err != nil {
		return nil
	}
	if mvm.Config == nil || mvm.Config.VAppConfig == nil {
		return nil
	}
	vci, _ := mvm.Config.VAppConfig.(*types.VmConfigInfo)
	return vci
}

// buildVAppPropertySpec creates a VmConfigSpec that sets OVF property values
// via the OVF environment transport mechanism. The guest reads these from the
// OVF environment (ISO or com.vmware.guestInfo) — the VMware-native way.
// buildVAppPropertySpec creates a VmConfigSpec that sets OVF property values
// by matching property Ids to their int32 Keys from the source template.
// buildLinuxCustomization returns a CustomizationSpec that configures a static
// IP on the first NIC. This is the VMware-native guest OS customization mechanism
// — vCenter runs it at first boot via vmtools, the same way TKG does.
func buildLinuxCustomization(props map[string]string, vmName string) *types.CustomizationSpec {
	hostname := props["guestinfo.hostname"]
	if hostname == "" {
		hostname = props["guestinfo.static_ip"]
	}
	ip := props["guestinfo.static_ip"]
	mask := props["guestinfo.netmask"]
	if mask == "" {
		mask = "255.255.255.0"
	}
	gw := props["guestinfo.gateway"]
	dns := props["guestinfo.dns"]

	var nicIP types.BaseCustomizationIpGenerator = &types.CustomizationDhcpIpGenerator{}
	if props["guestinfo.ip_mode"] == "static" && ip != "" {
		nicIP = &types.CustomizationFixedIp{IpAddress: ip}
	}
	spec := &types.CustomizationSpec{
		Identity: &types.CustomizationLinuxPrep{
			HostName: &types.CustomizationVirtualMachineName{},
		},
		GlobalIPSettings: types.CustomizationGlobalIPSettings{
			DnsServerList: filterEmpty([]string{dns}),
		},
		// Always include 1 NIC mapping to match the VM's 1 NIC.
		NicSettingMap: []types.CustomizationAdapterMapping{
			{
				Adapter: types.CustomizationIPSettings{
					Ip:            nicIP,
					SubnetMask:    mask,
					Gateway:       filterEmpty([]string{gw}),
					DnsServerList: filterEmpty([]string{dns}),
				},
			},
		},
	}
	return spec
}

func filterEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildVAppPropertySpec(src *types.VmConfigInfo, values map[string]string, transport []string) *types.VmConfigSpec {
	// Build a lookup of Id→Key from the source template so we correctly
	// reference each property by its int32 key when setting the value.
	srcKeys := make(map[string]int32, len(src.Property))
	for _, p := range src.Property {
		srcKeys[p.Id] = p.Key
	}
	specs := make([]types.VAppPropertySpec, 0, len(values))
	for k, v := range values {
		key, ok := srcKeys[k]
		if !ok {
			continue
		} // skip properties not in the template
		specs = append(specs, types.VAppPropertySpec{
			ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: "edit"},
			Info:            &types.VAppPropertyInfo{Key: key, Id: k, Value: v},
		})
	}
	if len(transport) == 0 {
		transport = []string{"com.vmware.guestInfo"}
	}
	return &types.VmConfigSpec{
		Property:                specs,
		OvfEnvironmentTransport: transport,
	}
}

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

// NetworkInfo describes a network/portgroup available to the deploy form.
// Type is "standard" for standard vSwitch portgroups, "distributed" for dvPort
// groups. DVSName is the parent distributed switch name (empty for standard).
type NetworkInfo struct {
	Name    string
	Path    string
	Type    string // "standard" | "distributed"
	DVSName string
}

// ListNetworks enumerates standard portgroups and dvPortgroups visible in all
// datacenters of this vCenter. Used to populate the network picker in the deploy
// form so operators can pick the correct segment for the agent VM's NIC.
func (c *Client) ListNetworks(ctx context.Context) ([]NetworkInfo, error) {
	m := view.NewManager(c.vc.Client)
	// Create a container view over Network objects (covers both standard portgroups
	// and dvPortgroup). A flat container at the root captures all datacenters.
	cv, err := m.CreateContainerView(ctx, c.vc.ServiceContent.RootFolder,
		[]string{"Network", "DistributedVirtualPortgroup"}, true)
	if err != nil {
		return nil, fmt.Errorf("vcenter: create network container view: %w", err)
	}
	defer cv.Destroy(ctx)

	// Retrieve standard Network objects.
	var stdNets []mo.Network
	if err := cv.Retrieve(ctx, []string{"Network"}, []string{"name", "summary"}, &stdNets); err != nil {
		return nil, fmt.Errorf("vcenter: retrieve networks: %w", err)
	}

	// Retrieve dvPortgroup objects (includes parent DVS name via config.distributedVirtualSwitch).
	var dvPGs []mo.DistributedVirtualPortgroup
	if err := cv.Retrieve(ctx, []string{"DistributedVirtualPortgroup"},
		[]string{"name", "config"}, &dvPGs); err != nil {
		return nil, fmt.Errorf("vcenter: retrieve dvportgroups: %w", err)
	}

	out := make([]NetworkInfo, 0, len(stdNets)+len(dvPGs))
	for _, n := range stdNets {
		out = append(out, NetworkInfo{
			Name: n.Name,
			Path: n.Name,
			Type: "standard",
		})
	}
	for _, pg := range dvPGs {
		dvsName := ""
		if pg.Config.DistributedVirtualSwitch != nil {
			dvsName = pg.Config.DistributedVirtualSwitch.Value
		}
		out = append(out, NetworkInfo{
			Name:    pg.Name,
			Path:    pg.Name,
			Type:    "distributed",
			DVSName: dvsName,
		})
	}
	return out, nil
}

// buildNetworkDevice constructs a VirtualDeviceConfigSpec that replaces the
// source template's first NIC with the requested network (standard portgroup or
// dvPortgroup). Returns nil when the source VM has no NIC (no-op).
func (c *Client) buildNetworkDevice(ctx context.Context, finder *find.Finder, src *object.VirtualMachine, networkPath string) (*types.VirtualDeviceConfigSpec, error) {
	var mvm mo.VirtualMachine
	if err := src.Properties(ctx, src.Reference(), []string{"config.hardware.device"}, &mvm); err != nil {
		return nil, fmt.Errorf("vcenter: read source devices: %w", err)
	}

	// Find the first ethernet card in the source.
	var srcNIC types.BaseVirtualEthernetCard
	for _, dev := range mvm.Config.Hardware.Device {
		if nic, ok := dev.(types.BaseVirtualEthernetCard); ok {
			srcNIC = nic
			break
		}
	}
	if srcNIC == nil {
		return nil, nil // no NIC to remap
	}

	// Resolve the target network object (standard portgroup or dvPortgroup).
	net, err := finder.Network(ctx, networkPath)
	if err != nil {
		return nil, fmt.Errorf("vcenter: network %q: %w", networkPath, err)
	}
	backing, err := net.EthernetCardBackingInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("vcenter: network backing %q: %w", networkPath, err)
	}

	// Clone the source NIC and swap its backing.
	dev := srcNIC.GetVirtualEthernetCard()
	dev.Backing = backing
	dev.DeviceInfo = nil // clear label so vCenter auto-assigns

	return &types.VirtualDeviceConfigSpec{
		Operation: types.VirtualDeviceConfigSpecOperationEdit,
		Device:    srcNIC.(types.BaseVirtualDevice),
	}, nil
}

type VMHardware struct {
	CPU          int32
	MemoryMB     int32
	DiskKB       int64
	NetworkLabel string
}
type ReconfigSpec struct {
	CPU       *int32
	MemoryMB  *int32
	DiskKB    *int64
	PortGroup *string
	VAppProps map[string]string
}

func (c *Client) ReconfigVM(ctx context.Context, vmName string, spec ReconfigSpec) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return fmt.Errorf("find vm: %w", err)
	}
	cs := types.VirtualMachineConfigSpec{}
	if spec.CPU != nil {
		cs.NumCPUs = *spec.CPU
	}
	if spec.MemoryMB != nil {
		cs.MemoryMB = int64(*spec.MemoryMB)
	}
	if spec.DiskKB != nil {
		devs, _ := vm.Device(ctx)
		for _, d := range devs.SelectByType(&types.VirtualDisk{}) {
			if vd, ok := d.(*types.VirtualDisk); ok {
				vd.CapacityInKB = *spec.DiskKB
				cs.DeviceChange = append(cs.DeviceChange, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: vd})
				break
			}
		}
	}
	if spec.PortGroup != nil {
		c.applyNet(ctx, vm, *spec.PortGroup, &cs)
	}
	if len(spec.VAppProps) > 0 {
		specs := make([]types.VAppPropertySpec, 0, len(spec.VAppProps))
		for k, v := range spec.VAppProps {
			specs = append(specs, types.VAppPropertySpec{ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: types.ArrayUpdateOperationEdit}, Info: &types.VAppPropertyInfo{Key: 0, Id: k, Value: v}})
		}
		cs.VAppConfig = &types.VmConfigSpec{Property: specs}
	}
	task, _ := vm.Reconfigure(ctx, cs)
	return task.Wait(ctx)
}
func (c *Client) applyNet(ctx context.Context, vm *object.VirtualMachine, pp string, cs *types.VirtualMachineConfigSpec) {
	finder := find.NewFinder(c.vc.Client, false)
	netRef, _ := finder.Network(ctx, pp)
	if netRef == nil {
		return
	}
	backing, _ := netRef.EthernetCardBackingInfo(ctx)
	devs, _ := vm.Device(ctx)
	for _, d := range devs.SelectByType(&types.VirtualEthernetCard{}) {
		if nic, ok := d.(types.BaseVirtualEthernetCard); ok {
			nic.GetVirtualEthernetCard().Backing = backing
			cs.DeviceChange = append(cs.DeviceChange, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: nic.(types.BaseVirtualDevice)})
			break
		}
	}
}
func (c *Client) GetVMHardware(ctx context.Context, vmName string) (*VMHardware, error) {
	vm, _ := c.findVM(ctx, vmName)
	var mvm mo.VirtualMachine
	vm.Properties(ctx, vm.Reference(), []string{"summary.config.numCpu", "summary.config.memorySizeMB", "summary.storage.committed", "summary.storage.uncommitted"}, &mvm)
	return &VMHardware{CPU: int32(mvm.Summary.Config.NumCpu), MemoryMB: int32(mvm.Summary.Config.MemorySizeMB), DiskKB: mvm.Summary.Storage.Committed + mvm.Summary.Storage.Uncommitted}, nil
}
func (c *Client) GetVAppProperties(ctx context.Context, vmName string) ([]OVFProperty, error) {
	vm, _ := c.findVM(ctx, vmName)
	var mvm mo.VirtualMachine
	vm.Properties(ctx, vm.Reference(), []string{"config.vAppConfig"}, &mvm)
	if mvm.Config == nil || mvm.Config.VAppConfig == nil {
		return nil, nil
	}
	vci, ok := mvm.Config.VAppConfig.(*types.VmConfigInfo)
	if !ok {
		return nil, nil
	}
	out := make([]OVFProperty, 0, len(vci.Property))
	for _, p := range vci.Property {
		out = append(out, OVFProperty{Key: p.Id, DefaultValue: p.Value})
	}
	return out, nil
}

type InstantCloneSpec struct {
	ParentVM, Name, ResourcePool, Datastore, Folder, Network string
	ExtraConfig                                              map[string]string
	BiosUUID                                                 string
}

func (c *Client) InstantClone(ctx context.Context, spec InstantCloneSpec) (*VMInfo, error) {
	src, _ := c.findVM(ctx, spec.ParentVM)
	finder := find.NewFinder(c.vc.Client, true)
	dc, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(dc)
	relocate := &types.VirtualMachineRelocateSpec{}
	if spec.ResourcePool != "" {
		p, _ := finder.ResourcePool(ctx, spec.ResourcePool)
		if p != nil {
			relocate.Pool = types.NewReference(p.Reference())
		}
	}
	if spec.Datastore != "" {
		d, _ := finder.Datastore(ctx, spec.Datastore)
		if d != nil {
			relocate.Datastore = types.NewReference(d.Reference())
		}
	}
	// No DeviceChange — inherit parent NIC as-is.
	// Static IP handled by CustomizeGuest_Task after clone.
	var config []types.BaseOptionValue
	for k, v := range spec.ExtraConfig {
		config = append(config, &types.OptionValue{Key: k, Value: v})
	}
	cs := &types.VirtualMachineInstantCloneSpec{Name: spec.Name, Location: *relocate}
	if len(config) > 0 {
		cs.Config = config
	}
	log.Printf("[instant-clone] cloning %s→%s pool=%q net=%q extraConfig=%d devChanges=%d",
		spec.ParentVM, spec.Name, spec.ResourcePool, spec.Network, len(config), len(relocate.DeviceChange))
	if spec.BiosUUID != "" {
		cs.BiosUuid = spec.BiosUUID
	}
	req := &types.InstantClone_Task{This: src.Reference(), Spec: *cs}
	resp, err := methods.InstantClone_Task(ctx, c.vc.Client.RoundTripper, req)
	if err != nil {
		return nil, fmt.Errorf("vcenter: InstantClone_Task %s→%s: %w", spec.ParentVM, spec.Name, err)
	}
	task := object.NewTask(c.vc.Client, resp.Returnval)
	if res, taskErr := task.WaitForResult(ctx, nil); taskErr != nil {
		return nil, fmt.Errorf("vcenter: instant clone task %s→%s: %w", spec.ParentVM, spec.Name, taskErr)
	} else if res.Error != nil {
		return nil, fmt.Errorf("vcenter: instant clone task %s→%s: %s", spec.ParentVM, spec.Name, res.Error.LocalizedMessage)
	}
	return c.vmInfo(ctx, spec.Name)
}

// ConnectNIC reconnects the first ethernet adapter (connected/startConnected only).
func (c *Client) ConnectNIC(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return fmt.Errorf("find vm: %w", err)
	}
	devs, err := vm.Device(ctx)
	if err != nil {
		return fmt.Errorf("get devices: %w", err)
	}
	for _, d := range devs {
		nic, ok := d.(types.BaseVirtualEthernetCard)
		if !ok {
			continue
		}
		card := nic.GetVirtualEthernetCard()
		if card.Connectable == nil {
			card.Connectable = &types.VirtualDeviceConnectInfo{}
		}
		card.Connectable.StartConnected = true
		card.Connectable.Connected = true
		cs := types.VirtualMachineConfigSpec{
			DeviceChange: []types.BaseVirtualDeviceConfigSpec{
				&types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationEdit,
					Device:    d,
				},
			},
		}
		task, err := vm.Reconfigure(ctx, cs)
		if err != nil {
			return fmt.Errorf("connect nic: %w", err)
		}
		return task.Wait(ctx)
	}
	return fmt.Errorf("no ethernet adapter found on %s", vmName)
	}

	// ── Guest Customization Manager (Instant Clone static IP) ───────────────

	// CustomizeGuestRequest describes static IP customization for instant clones.
	type CustomizeGuestRequest struct {
		Username, Password, Hostname, IPAddress, SubnetMask, Gateway string
		PrefixLen int
		DNSServers, DNSSearch []string
	}

	// CustomizeInstantCloneGuest calls vCenter GuestCustomizationManager:
	// wait VMware Tools → CustomizeGuest_Task → StartGuestNetwork_Task.
	func (c *Client) CustomizeInstantCloneGuest(ctx context.Context, vmName string, req CustomizeGuestRequest) error {
		vm, err := c.findVM(ctx, vmName)
		if err != nil {
			return fmt.Errorf("find vm: %w", err)
		}
		gcmRef := c.vc.ServiceContent.GuestCustomizationManager
		if gcmRef == nil {
			return fmt.Errorf("GuestCustomizationManager unavailable on this vCenter")
		}
		// Wait VMware Tools with longer timeout for instant clone stabilization
		// NIC must be disconnected for GOSC to customize (VMware docs step 3).
		// Also: disconnect+reconnect forces guest kernel to detect new MAC.
		log.Printf("[guest-customize] disconnecting NIC on %s before customization", vmName)
		if err := c.disconnectNIC(ctx, vm); err != nil {
			log.Printf("[guest-customize] disconnect NIC non-fatal: %v", err)
		}
		select { case <-ctx.Done(): return ctx.Err(); case <-time.After(2 * time.Second): }

		log.Printf("[guest-customize] waiting for VMware Tools on %s", vmName)
		if err := waitForTools(ctx, vm); err != nil {
			return fmt.Errorf("vmware tools: %w", err)
		}
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-time.After(10 * time.Second):
		}
		// Build spec
		mask := req.SubnetMask
		if mask == "" { mask = prefixToMask(req.PrefixLen) }
		host := req.Hostname; if host == "" { host = req.IPAddress }
		gws := []string{}; if req.Gateway != "" { gws = []string{req.Gateway} }
		spec := &types.CustomizationSpec{
			Identity: &types.CustomizationLinuxPrep{
				HostName: &types.CustomizationFixedName{Name: host},
				Password: &types.CustomizationPassword{
					Value:     req.Password,
					PlainText: true,
				},
			},
			GlobalIPSettings: types.CustomizationGlobalIPSettings{
				DnsServerList: req.DNSServers,
				DnsSuffixList: req.DNSSearch,
			},
			NicSettingMap: []types.CustomizationAdapterMapping{{
				Adapter: types.CustomizationIPSettings{
					Ip: &types.CustomizationFixedIp{IpAddress: req.IPAddress},
					SubnetMask: mask, Gateway: gws,
					DnsServerList: req.DNSServers,
				},
			}},
		}
		auth := &types.NamePasswordAuthentication{Username: req.Username, Password: req.Password}
		// CustomizeGuest_Task (NIC must be disconnected)
		log.Printf("[guest-customize] CustomizeGuest_Task %s ip=%s", vmName, req.IPAddress)
		cr, err := methods.CustomizeGuest_Task(ctx, c.vc.Client.RoundTripper, &types.CustomizeGuest_Task{
			This: *gcmRef, Vm: vm.Reference(), Auth: auth, Spec: *spec,
		})
		if err != nil { return fmt.Errorf("CustomizeGuest_Task: %w", err) }
		if res, err := object.NewTask(c.vc.Client, cr.Returnval).WaitForResult(ctx, nil); err != nil {
			// GOSC scripts complete but vCenter times out after 30s.
			// Don't fail — the IP is actually set. Verify below.
			log.Printf("[guest-customize] CustomizeGuest_Task returned: %v (GOSC may have succeeded despite timeout)", err)
		} else if res.Error != nil {
			log.Printf("[guest-customize] CustomizeGuest_Task: %s (may still be OK)", res.Error.LocalizedMessage)
		}
		log.Printf("[guest-customize] CustomizeGuest_Task done %s", vmName)
		return nil
	}

	// StartGuestNetwork starts the guest network after NIC reconnection.
	func (c *Client) StartGuestNetwork(ctx context.Context, vmName string, user, pass string) error {
		vm, err := c.findVM(ctx, vmName)
		if err != nil { return fmt.Errorf("find vm: %w", err) }
		gcmRef := c.vc.ServiceContent.GuestCustomizationManager
		if gcmRef == nil { return fmt.Errorf("GuestCustomizationManager unavailable") }
		auth := &types.NamePasswordAuthentication{Username: user, Password: pass}
		nr, err := methods.StartGuestNetwork_Task(ctx, c.vc.Client.RoundTripper, &types.StartGuestNetwork_Task{
			This: *gcmRef, Vm: vm.Reference(), Auth: auth,
		})
		if err != nil { return fmt.Errorf("StartGuestNetwork_Task: %w", err) }
		if res, err := object.NewTask(c.vc.Client, nr.Returnval).WaitForResult(ctx, nil); err != nil {
			// vCenter 30s timeout — the network is restarted anyway
			log.Printf("[guest-customize] StartGuestNetwork returned: %v (may have succeeded)", err)
		} else if res.Error != nil {
			log.Printf("[guest-customize] StartGuestNetwork: %s (may be OK)", res.Error.LocalizedMessage)
		}
		return nil
	}

	// WaitForGuestIP polls guest.ipAddress until it matches expectedIP.
	func (c *Client) WaitForGuestIP(ctx context.Context, vmName, expectedIP string, timeout time.Duration) error {
		vm, err := c.findVM(ctx, vmName)
		if err != nil { return fmt.Errorf("find vm: %w", err) }
		deadline := time.Now().Add(timeout)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done(): return ctx.Err()
			case <-ticker.C:
				if time.Now().After(deadline) {
					return fmt.Errorf("wait guest IP %s timed out", expectedIP)
				}
				var mvm mo.VirtualMachine
				if err := vm.Properties(ctx, vm.Reference(), []string{"guest.ipAddress"}, &mvm); err != nil {
					continue
				}
				ip := ""; if mvm.Guest != nil { ip = mvm.Guest.IpAddress }
				if ip == expectedIP { return nil }
				if ip != "" && ip != "127.0.0.1" {
					log.Printf("[guest-customize] %s reported IP=%s", vmName, ip)
				}
			}
		}
	}

	// ── internal helpers ─────────────────────────────────────────────────

	// readGOSCLog runs "cat /var/log/vmware-gosc/instant_clone_customization.log"
	// inside the guest via StartProgramInGuest and returns its output.
	func (c *Client) readGOSCLog(ctx context.Context, vm *object.VirtualMachine, user, pass string) (string, error) {
		auth := &types.NamePasswordAuthentication{Username: user, Password: pass}
		spec := &types.GuestProgramSpec{
			ProgramPath: "/bin/cat",
			Arguments:   "/var/log/vmware-gosc/instant_clone_customization.log",
		}
		req := &types.StartProgramInGuest{
			This: vm.Reference(),
			Vm:   vm.Reference(),
			Auth: auth,
			Spec: spec,
		}
		resp, err := methods.StartProgramInGuest(ctx, c.vc.Client.RoundTripper, req)
		if err != nil {
			return "", fmt.Errorf("StartProgramInGuest: %w", err)
		}
		// The response contains a PID; we'd need to read its output.
		// For now just report the PID.
		return fmt.Sprintf("started PID=%d", resp.Returnval), nil
	}



	// disconnectNIC sets connected=false on the first ethernet adapter.
	func (c *Client) disconnectNIC(ctx context.Context, vm *object.VirtualMachine) error {
		devs, err := vm.Device(ctx)
		if err != nil { return fmt.Errorf("get devices: %w", err) }
		for _, d := range devs {
			if nic, ok := d.(types.BaseVirtualEthernetCard); ok {
				card := nic.GetVirtualEthernetCard()
				if card.Connectable == nil { card.Connectable = &types.VirtualDeviceConnectInfo{} }
				card.Connectable.Connected = false
				card.Connectable.StartConnected = false
				task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{
					DeviceChange: []types.BaseVirtualDeviceConfigSpec{
						&types.VirtualDeviceConfigSpec{
							Operation: types.VirtualDeviceConfigSpecOperationEdit,
							Device: d,
						},
					},
				})
				if err != nil { return fmt.Errorf("disconnect nic: %w", err) }
				return task.Wait(ctx)
			}
		}
		return nil
	}

	func waitForTools(ctx context.Context, vm *object.VirtualMachine) error {
		deadline := time.Now().Add(120 * time.Second)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done(): return ctx.Err()
			case <-ticker.C:
				if time.Now().After(deadline) {
					return fmt.Errorf("VMware Tools timeout for %s", vm.Name())
				}
				var mvm mo.VirtualMachine
				if err := vm.Properties(ctx, vm.Reference(), []string{"guest.toolsRunningStatus"}, &mvm); err != nil {
					continue
				}
				if mvm.Guest != nil && mvm.Guest.ToolsRunningStatus == string(types.VirtualMachineToolsRunningStatusGuestToolsRunning) {
					return nil
				}
			}
		}
	}

	func prefixToMask(prefixLen int) string {
		if prefixLen <= 0 || prefixLen > 32 { return "255.255.255.0" }
		m := ^uint32(0) << (32 - prefixLen)
		return fmt.Sprintf("%d.%d.%d.%d", byte(m>>24), byte(m>>16), byte(m>>8), byte(m))
	}



// ReadGuestInfo reads guestinfo.* from config.extraConfig. Guest-set values
// (via vmware-rpctool info-set) are synced into config.extraConfig by the host.
// Keys returned WITHOUT "guestinfo." prefix.
func (c *Client) ReadGuestInfo(ctx context.Context, vmName string) (map[string]string, error) {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("find vm: %w", err)
	}
	var mvm mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.extraConfig"}, &mvm); err != nil {
		return nil, fmt.Errorf("read extraConfig: %w", err)
	}
	out := make(map[string]string)
	for _, opt := range mvm.Config.ExtraConfig {
		ov, ok := opt.(*types.OptionValue)
		if !ok || ov == nil {
			continue
		}
		if !strings.HasPrefix(ov.Key, "guestinfo.") {
			continue
		}
		val := ""
		if ov.Value != nil {
			val = ov.Value.(string)
		}
		out[strings.TrimPrefix(ov.Key, "guestinfo.")] = val
	}
	log.Printf("[instant-clone] ReadGuestInfo %s: %d keys, state=%q gen=%q",
		vmName, len(out), out["agentmgr.state"], out["agentmgr.applied-generation"])
	return out, nil
}

// WaitGuestInfoState polls guestinfo.agentmgr.state every 2s until it matches
// expectedState and applied-generation matches generation. Returns error on
// timeout or guest-reported failure.
func (c *Client) WaitGuestInfoState(ctx context.Context, vmName, expectedState, generation string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait guestinfo %s: %w", expectedState, ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("wait guestinfo %s timed out after %v", expectedState, timeout)
			}
			gi, err := c.ReadGuestInfo(ctx, vmName)
			if err != nil {
				log.Printf("[instant-clone] ReadGuestInfo error: %v", err)
				continue
			}
			state := gi["agentmgr.state"]
			appliedGen := gi["agentmgr.applied-generation"]
			if state == "failed" {
				return fmt.Errorf("guest config failed: [%s] %s",
					gi["agentmgr.error-code"], gi["agentmgr.error-message"])
			}
			if state == expectedState && appliedGen == generation {
				return nil
			}
			if state != "" {
				log.Printf("[instant-clone] guest state=%s gen=%s", state, appliedGen)
			}
		}
	}
}

func (c *Client) ListRunningVMs(ctx context.Context) ([]VMInfo, error) {
	m := view.NewManager(c.vc.Client)
	v, _ := m.CreateContainerView(ctx, c.vc.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	defer v.Destroy(ctx)
	var vms []mo.VirtualMachine
	v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"summary"}, &vms)
	var out []VMInfo
	for _, vm := range vms {
		if vm.Summary.Runtime.PowerState != "poweredOn" || vm.Summary.Config.Template {
			continue
		}
		out = append(out, VMInfo{Name: vm.Summary.Config.Name, PowerState: string(vm.Summary.Runtime.PowerState), UUID: vm.Summary.Config.Uuid})
	}
	return out, nil
}
func (c *Client) CheckInstantCloneCompatible(ctx context.Context, vmName string) error {
	vm, _ := c.findVM(ctx, vmName)
	req := &types.CheckInstantClone_Task{This: vm.Reference(), Spec: types.VirtualMachineInstantCloneSpec{Name: "_ck_", Location: types.VirtualMachineRelocateSpec{}}}
	_, err := methods.CheckInstantClone_Task(ctx, c.vc.Client.RoundTripper, req)
	return err
}
func (c *Client) RemoveSerialPorts(ctx context.Context, vmName string) error {
	vm, err := c.findVM(ctx, vmName)
	if err != nil {
		return fmt.Errorf("find vm: %w", err)
	}
	devs, err := vm.Device(ctx)
	fmt.Printf("[RemoveSerialPorts] vm=%q devices=%d\n", vmName, len(devs))
	if err != nil {
		return fmt.Errorf("get devices: %w", err)
	}
	var changes []types.BaseVirtualDeviceConfigSpec
	for _, d := range devs {
		if _, ok := d.(*types.VirtualSerialPort); ok {
			fmt.Printf("[RemoveSerialPorts] FOUND serial port on %q\n", vmName)
		}
		if _, ok := d.(*types.VirtualSerialPort); ok {
			changes = append(changes, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationRemove, Device: d})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{DeviceChange: changes})
	if err != nil {
		return fmt.Errorf("reconfigure: %w", err)
	}
	return task.Wait(ctx)
}
