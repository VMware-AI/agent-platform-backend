// Package deploy orchestrates agent-VM provisioning: it issues a per-user
// gateway key, builds the cloud-init payload, and injects it into the target VM
// via vCenter guestinfo. This is the vertical that connects the gateway and
// vcenter clients. See LLD-05 §3.
package deploy

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// VMProvisioner is the slice of the vCenter client this package needs to stand
// up an agent VM end-to-end (satisfied by *vcenter.Client). Kept narrow for
// testability.
type VMProvisioner interface {
	CloneFromTemplate(ctx context.Context, spec vcenter.CloneSpec) (*vcenter.VMInfo, error)
	SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error
	PowerOn(ctx context.Context, vmName string) error
	Destroy(ctx context.Context, vmName string) error
}

// Service provisions agent VMs by wiring the gateway + vCenter together.
type Service struct {
	Gateway    gateway.Client
	VCenter    VMProvisioner
	GatewayURL string // LLM gateway base URL injected into the VM (e.g. https://gateway.internal)
}

// Request describes a single agent-VM provisioning: clone the VM from an OVA
// template, inject its per-VM config, and power it on.
type Request struct {
	AgentName    string
	UserID       string
	TeamID       string
	Template     string // source OVA template to clone from
	VMName       string // new VM name to create
	ResourcePool string // target resource pool ("" = inherit template's pool)
	Network      string // target portgroup path ("" = keep source template's NIC)
	Hostname     string
	Models       []string
	MaxBudget    *float64
	// DefaultConfig is the agent's inline default_config (LLD-09); when set it is
	// embedded into cloud-init at ConfigPath so the VM never fetches it.
	DefaultConfig string
	ConfigPath    string
	// agent-manager enrollment (LLD-08): a one-time enroll token + stable vm id
	// injected via guestinfo for the daemon to exchange on first boot. Empty when
	// agent-manager is not configured.
	VMID            string
	EnrollToken     string
	ControlPlaneURL string
	// KnowledgePackIDs are the OKF knowledge artifacts mounted on the agent's
	// config (LLD-11 K2). Their ids are handed to the VM via guestinfo; the daemon
	// pulls each bundle over the control-plane channel (§6). Requires the
	// agent-manager channel (EnrollToken) — without it there is no way to fetch.
	KnowledgePackIDs []string
	// KnowledgeRoot is the VM dir the daemon unpacks knowledge packs to (LLD-11 K4,
	// per-kind via AgentTemplate). Only injected when packs are present.
	KnowledgeRoot string
	// OVFProperties carries user-provided vApp/OVF property values from the deploy
	// form. guestinfo.* keys are injected as VM ExtraConfig at clone time.
	OVFProperties map[string]string
}

// Result carries the issued secret (returned once), the rendered userdata, and
// the created VM name.
type Result struct {
	VirtualKey string // secret — surface once, never persist in plaintext
	// VirtualKeyToken is the gateway's hashed key identifier (what /key/list
	// returns); persisted so reconciliation can match the row. Empty if omitted.
	VirtualKeyToken string
	Userdata        string
	VMName          string
}

// Provision issues a key, clones the agent VM from the OVA template, injects
// cloud-init via guestinfo, and powers it on. On any step's failure it rolls
// back the work already done so no orphan VM or gateway key is left (LLD-05 §3).
//
// DEV_NO_VCENTER: when set (e.g. "1" or "true"), skips all vCenter operations
// and returns the issued key immediately. The agent row is created but the VM
// does not exist. This is for frontend/UI development without a live vCenter.
func (s *Service) Provision(ctx context.Context, req Request) (*Result, error) {
	if s.Gateway == nil {
		return nil, fmt.Errorf("deploy: gateway must be configured")
	}
	// Dev mode: vCenter is optional; gateway is always required.
	if os.Getenv("DEV_NO_VCENTER") != "1" && os.Getenv("DEV_NO_VCENTER") != "true" {
		if s.VCenter == nil {
			return nil, fmt.Errorf("deploy: gateway and vcenter must be configured")
		}
	}
	if req.VMName == "" || req.UserID == "" || req.Template == "" {
		return nil, fmt.Errorf("deploy: template, vmName and userId are required")
	}
	models := req.Models
	if len(models) == 0 {
		models = []string{gateway.DefaultRouterModel} // difficulty router by default (LLD-04)
	}

	// 1) Issue the gateway key (cheap and revocable — do it before the VM).
	key, err := s.Gateway.GenerateKey(ctx, gateway.GenerateKeyRequest{
		UserID:    req.UserID,
		TeamID:    req.TeamID,
		Models:    models,
		MaxBudget: req.MaxBudget,
		Metadata:  map[string]string{"agent": req.AgentName},
	})
	if err != nil {
		return nil, fmt.Errorf("deploy: issue key: %w", err)
	}

	// Dev mode: skip VM provisioning, return the key immediately.
	if os.Getenv("DEV_NO_VCENTER") == "1" || os.Getenv("DEV_NO_VCENTER") == "true" {
		return &Result{VirtualKey: key.Key, VirtualKeyToken: key.Token, VMName: req.VMName}, nil
	}

	// 2) Clone the agent VM from the OVA template, powered off so guestinfo is
	//    set before first boot. OVF properties with guestinfo.* keys are injected
	//    as ExtraConfig at clone time; the template's vApp/OVF reads them on boot.
	if _, err := s.VCenter.CloneFromTemplate(ctx, vcenter.CloneSpec{
		Template:     req.Template,
		Name:         req.VMName,
		ResourcePool: req.ResourcePool,
		Network:      req.Network,
		PowerOn:      false,
		ExtraConfig:  req.OVFProperties,
			VAppProperties: req.OVFProperties,
	}); err != nil {
		s.revokeKey(ctx, key.Key) // no VM to clean up yet
		return nil, fmt.Errorf("deploy: clone template: %w", err)
	}

	// 3) Inject per-VM cloud-init via guestinfo.
	userdata := buildUserdata(s.GatewayURL, key.Key, req.Hostname, req.DefaultConfig, req.ConfigPath, req.OVFProperties)
	gi := map[string]string{"userdata": userdata}
	// Static IP: inject network-config so VMware's cloud-init datasource
	// picks it up. Also set a fresh instance-id to force cloud-init to run.
	if req.OVFProperties != nil && req.OVFProperties["guestinfo.ip_mode"] == "static" {
		nc := buildNetplanConfig(req.OVFProperties)
		if nc != "" {
			gi["network-config"] = nc
		}
		// Fresh instance-id forces cloud-init to treat this as a new instance.
		gi["instance-id"] = req.VMName
	}
	if req.Hostname != "" {
		gi["metadata"] = buildMetadata(req.Hostname)
	}
	// agent-manager enrollment (LLD-08 §4.2/§10): hand the daemon a one-time
	// enroll token + its stable id + the control-plane URL via guestinfo. The
	// token is short-lived and single-use; SetGuestinfo base64-encodes values.
	if req.EnrollToken != "" {
		gi["agentmgr.enroll_token"] = req.EnrollToken
		gi["agentmgr.vm_id"] = req.VMID
		if req.ControlPlaneURL != "" {
			gi["agentmgr.control_plane_url"] = req.ControlPlaneURL
		}
		// LLD-11 §6: tell the daemon which knowledge packs to pull (by id) over the
		// same authenticated channel. Only meaningful with the agent-manager channel.
		if len(req.KnowledgePackIDs) > 0 {
			gi["agentmgr.knowledge_packs"] = strings.Join(req.KnowledgePackIDs, ",")
			if req.KnowledgeRoot != "" {
				gi["agentmgr.knowledge_root"] = req.KnowledgeRoot // LLD-11 K4 per-kind unpack dir
			}
		}
	}
	if err := s.VCenter.SetGuestinfo(ctx, req.VMName, gi); err != nil {
		s.rollback(ctx, key.Key, req.VMName)
		return nil, fmt.Errorf("deploy: inject guestinfo: %w", err)
	}

	// 4) Power on — cloud-init consumes guestinfo at first boot.
	if err := s.VCenter.PowerOn(ctx, req.VMName); err != nil {
		s.rollback(ctx, key.Key, req.VMName)
		return nil, fmt.Errorf("deploy: power on: %w", err)
	}

	return &Result{VirtualKey: key.Key, VirtualKeyToken: key.Token, Userdata: userdata, VMName: req.VMName}, nil
}

// rollback tears down a half-provisioned agent: destroy the cloned VM and revoke
// the key. Uses a detached context so cleanup runs even if ctx was canceled.
// Cleanup failures are logged (not swallowed) so an operator can find the orphan.
func (s *Service) rollback(ctx context.Context, key, vmName string) {
	cctx := context.WithoutCancel(ctx)
	if err := s.VCenter.Destroy(cctx, vmName); err != nil {
		log.Printf("deploy rollback: orphan VM %q, destroy failed: %v", vmName, err)
	}
	if err := s.Gateway.DeleteKey(cctx, key); err != nil {
		log.Printf("deploy rollback: orphan gateway key, revoke failed: %v", err)
	}
}

// revokeKey revokes an issued key when there is no VM yet to clean up.
func (s *Service) revokeKey(ctx context.Context, key string) {
	if err := s.Gateway.DeleteKey(context.WithoutCancel(ctx), key); err != nil {
		log.Printf("deploy: orphan gateway key after clone failure, revoke failed: %v", err)
	}
}

// buildUserdata renders the cloud-init that drops the gateway env (LLD-05 §3)
// and, when an inline default_config is present, embeds it as a second
// write_files entry so the VM gets its config without any network fetch (LLD-09).
// buildNetplanConfig renders a netplan YAML for static IP configuration.
// validIP returns true for dotted-quad IPv4 addresses.
func validIP(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' { continue }
		if c == '.' { continue }
		return false
	}
	return len(s) >= 7 && len(s) <= 15
}

func buildNetplanConfig(props map[string]string) string {
	ip := props["guestinfo.static_ip"]
	if ip == "" || !validIP(ip) { return "" }
	mask := props["guestinfo.netmask"]
	if mask == "" { mask = "255.255.255.0" }
	cidr := toCIDR(ip, mask)
	gw := props["guestinfo.gateway"]
	if gw != "" && !validIP(gw) { gw = "" }
	dns := props["guestinfo.dns"]
	if dns != "" && !validIP(dns) { dns = "" }

	var b strings.Builder
	b.WriteString("network: {version: 2, renderer: networkd}\n")
	b.WriteString("ethernets:\n")
	b.WriteString("  id0:\n")
	b.WriteString("    match:\n")
	b.WriteString("      name: e*\n")
	b.WriteString("    dhcp4: false\n")
	b.WriteString(fmt.Sprintf("    addresses: [%s]\n", cidr))
	if gw != "" {
		b.WriteString(fmt.Sprintf("    routes: [{to: default, via: %s}]\n", gw))
	}
	if dns != "" {
		b.WriteString(fmt.Sprintf("    nameservers: {addresses: [%s]}\n", dns))
	}
	return b.String()
}

func buildUserdata(gatewayURL, key, hostname, defaultConfig, configPath string, ovfProps map[string]string) string {
	base := strings.TrimRight(gatewayURL, "/")
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", hostname)
	}
	
	// Static IP: write a netplan YAML file so systemd-networkd picks it up.
	// The network: section in cloud-init userdata is not reliably processed by
	// the VMware GuestInfo datasource, so we drop a file into /etc/netplan instead.
	if ovfProps != nil && ovfProps["guestinfo.ip_mode"] == "static" {
		ip := ovfProps["guestinfo.static_ip"]
		mask := ovfProps["guestinfo.netmask"]
		if mask == "" { mask = "255.255.255.0" }
		cidr := toCIDR(ip, mask)
		gw := ovfProps["guestinfo.gateway"]
		dns := ovfProps["guestinfo.dns"]

		b.WriteString("  - path: /etc/netplan/01-static.yaml\n")
		b.WriteString("    permissions: \"0644\"\n")
		b.WriteString("    content: |\n")
		b.WriteString("      network: {version: 2, renderer: networkd}\n")
		b.WriteString("      ethernets:\n")
		b.WriteString("        eth0:\n")
		b.WriteString("          dhcp4: false\n")
		b.WriteString("          addresses: [")
		b.WriteString(cidr)
		b.WriteString("]\n")
		if gw != "" {
			b.WriteString("          routes: [{to: default, via: ")
			b.WriteString(gw)
			b.WriteString("}]\n")
		}
		if dns != "" {
			b.WriteString("          nameservers: {addresses: [")
			b.WriteString(dns)
			b.WriteString("]}\n")
		}
		b.WriteString("  - path: /etc/netplan/01-static.yaml.license\n")
		b.WriteString("    permissions: \"0644\"\n")
		b.WriteString("    content: auto-deployed by agent platform\n")
	}
	b.WriteString("write_files:\n")
	b.WriteString("  - path: /etc/agent/llm-gateway.env\n")
	b.WriteString("    permissions: \"0640\"\n")
	b.WriteString("    owner: root:agent\n")
	b.WriteString("    content: |\n")
	fmt.Fprintf(&b, "      OPENAI_BASE_URL=%s/v1\n", base)
	fmt.Fprintf(&b, "      OPENAI_API_KEY=%s\n", key)
	if defaultConfig != "" && configPath != "" {
		b.WriteString("  - path: ")
		b.WriteString(configPath)
		b.WriteString("\n")
		b.WriteString("    permissions: \"0640\"\n")
		b.WriteString("    owner: root:agent\n")
		b.WriteString("    content: |\n")
		// Indent each line by 6 spaces under the cloud-init block scalar.
		for _, line := range strings.Split(defaultConfig, "\n") {
			b.WriteString("      ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	
	// Apply netplan if static IP netplan file was written.
	if ovfProps != nil && ovfProps["guestinfo.ip_mode"] == "static" {
		b.WriteString("runcmd:\n")
		b.WriteString("  - [netplan, apply]\n")
	}
	return b.String()
}

// toCIDR converts an IP and netmask to CIDR notation (e.g. 172.16.85.199 + 255.255.255.0 → 172.16.85.199/24).
func toCIDR(ip, mask string) string {
	parts := strings.Split(mask, ".")
	if len(parts) != 4 {
		return ip + "/24"
	}
	ones := 0
	for _, p := range parts {
		var b int
		fmt.Sscanf(p, "%d", &b)
		for i := 7; i >= 0; i-- {
			if b&(1<<uint(i)) != 0 { ones++ }
		}
	}
	return fmt.Sprintf("%s/%d", ip, ones)
}

func buildMetadata(hostname string) string {
	return fmt.Sprintf("local-hostname: %s\n", hostname)
}
