// Package deploy orchestrates agent-VM provisioning: it issues a per-user
// gateway key, builds the cloud-init payload, and injects it into the target VM
// via vCenter guestinfo. This is the vertical that connects the gateway and
// vcenter clients. See LLD-05 §3.
package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// VMProvisioner is the slice of the vCenter client this package needs to stand
// up an agent VM end-to-end (satisfied by *vcenter.Client). Kept narrow for
// testability.
type VMProvisioner interface {
	CloneFromTemplate(ctx context.Context, spec vcenter.CloneSpec) (*vcenter.VMInfo, error)
	DeployOVF(ctx context.Context, spec vcenter.OVFDeploySpec) (*vcenter.VMInfo, error)
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
	// AgentPkgName is the agent package base name (guestinfo agent_name; the
	// daemon resolves {pkg_base_url}/{name}-{version}.tar.gz). NOT the display
	// name in AgentName above.
	AgentPkgName string
	// AgentVersion is the first-boot install target (guestinfo agent_version,
	// LLD-16 §3). Empty → the daemon skips auto-install (assumes pre-installed).
	AgentVersion string
	// AgentService is the systemd unit the daemon restarts on upgrade (daemon
	// default agent.service).
	AgentService string
	// AgentInstallRoot is the versioned install tree root holding versions/<v>
	// + current (daemon default /opt/agent).
	AgentInstallRoot string
	// AgentPkgBaseURL is the internal artifact mirror base for upgrades
	// (AGENT_PKG_BASE_URL). May embed read-only mirror credentials
	// (ftp://user:pass@...) — never log it.
	AgentPkgBaseURL string
	// AgentKeepVersions is how many installed versions the VM retains when
	// pruning after an upgrade (daemon default 3). 0 = don't stamp.
	AgentKeepVersions int
	// OVFProperties carries user-provided vApp/OVF property values from the deploy
	// form. guestinfo.* keys are injected as VM ExtraConfig at clone time.
	OVFProperties map[string]string
	// ContentLibraryItemID, when set, switches from CloneFromTemplate to OVF deploy
	// via vCenter's content-library deploy pipeline (native OVF environment).
	ContentLibraryItemID string
	// HostMoRef is the target host managed object reference for OVF deploy.
	HostMoRef string
	// FolderMoRef is the target VM folder managed object reference for OVF deploy.
	FolderMoRef string
	// ExistingKey, when set, reuses an existing gateway virtual key instead of
	// issuing a new one. The key must be unbound (not attached to any agent).
	ExistingKey string
	// ExistingKeyToken is the gateway's hashed key identifier for reconciliation.
	ExistingKeyToken string
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
		// Default to empty — models are operator-curated per agent, and the
		// "smart" complexity router alias is no longer the entry point.
	}

	// 1) Issue or reuse gateway key.
	var key gateway.KeyResponse
	if req.ExistingKey != "" {
		// Reuse an existing unbound virtual key — no GenerateKey call.
		key.Key = req.ExistingKey
		key.Token = req.ExistingKeyToken
	} else {
		k, err := s.Gateway.GenerateKey(ctx, gateway.GenerateKeyRequest{
			UserID:    req.UserID,
			TeamID:    req.TeamID,
			Models:    models,
			MaxBudget: req.MaxBudget,
			Metadata:  map[string]any{"agent": req.AgentName},
		})
		if err != nil {
			return nil, fmt.Errorf("deploy: issue key: %w", err)
		}
		key = *k
	}

	// Auto-detect static IP mode: if a static_ip is provided but ip_mode is not
	// explicitly set, default to "static". This ensures both the vCenter
	// CustomizationSpec (buildLinuxCustomization) and the cloud-init network-config
	// (buildNetplanConfig) pick up the static IP configuration.
	if req.OVFProperties != nil && req.OVFProperties["guestinfo.static_ip"] != "" && req.OVFProperties["guestinfo.ip_mode"] == "" {
		req.OVFProperties["guestinfo.ip_mode"] = "static"
	}

	// Dev mode: skip VM provisioning, return the key immediately.
	if os.Getenv("DEV_NO_VCENTER") == "1" || os.Getenv("DEV_NO_VCENTER") == "true" {
		return &Result{VirtualKey: key.Key, VirtualKeyToken: key.Token, VMName: req.VMName}, nil
	}

	// 2) Deploy the agent VM. Two paths:
	//    a) OVF deploy from content library (native OVF environment → guest reads ovf-env.xml).
	//    b) Clone from VM template (guestinfo/cloud-init injection).
	if req.ContentLibraryItemID != "" {
		if _, err := s.VCenter.DeployOVF(ctx, vcenter.OVFDeploySpec{
			LibraryItemID: req.ContentLibraryItemID,
			Name:          req.VMName,
			ResourcePool:  "resgroup-10",
			Host:          req.HostMoRef,
			Folder:        req.FolderMoRef,
			Datastore:     "",
			Properties:    req.OVFProperties,
			Network:       req.Network,
		}); err != nil {
			s.revokeKey(ctx, key.Key)
			return nil, fmt.Errorf("deploy: OVF deploy: %w", err)
		}
	} else {
		if _, err := s.VCenter.CloneFromTemplate(ctx, vcenter.CloneSpec{
			Template:       req.Template,
			Name:           req.VMName,
			ResourcePool:   req.ResourcePool,
			Network:        req.Network,
			PowerOn:        false,
			ExtraConfig:    req.OVFProperties,
			VAppProperties: req.OVFProperties,
		}); err != nil {
			s.revokeKey(ctx, key.Key) // no VM to clean up yet
			return nil, fmt.Errorf("deploy: clone template: %w", err)
		}
	}

	// 3) Inject per-VM cloud-init via guestinfo.
	userdata := buildUserdata(s.GatewayURL, key.Key, req.Hostname, req.DefaultConfig, req.ConfigPath, req.OVFProperties, req.Models)
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
	// token is short-lived and single-use. The daemon's rpctool reads consume
	// guestinfo verbatim, so SetGuestinfo writes these raw (only cloud-init's
	// userdata/metadata are base64-wrapped).
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
	// webadmin deploy-time contract: credential seed + in-VM upgrade wiring.
	// Stamped independent of the enroll channel — the VM-local webadmin consumes
	// these at first boot; absent keys fall back to the daemon's defaults.
	// agent_pkg_base_url may embed read-only mirror credentials: never log it
	// (guestinfo is a VM-readable, low-trust channel).
	if req.AgentPkgName != "" {
		gi["agentmgr.agent_name"] = req.AgentPkgName
	}
	if req.AgentVersion != "" {
		gi["agentmgr.agent_version"] = req.AgentVersion
	}
	if req.AgentService != "" {
		gi["agentmgr.agent_service"] = req.AgentService
	}
	if req.AgentInstallRoot != "" {
		gi["agentmgr.agent_install_root"] = req.AgentInstallRoot
	}
	if req.AgentPkgBaseURL != "" {
		gi["agentmgr.agent_pkg_base_url"] = req.AgentPkgBaseURL
	}
	if req.AgentKeepVersions > 0 {
		gi["agentmgr.agent_keep_versions"] = strconv.Itoa(req.AgentKeepVersions)
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
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' {
			continue
		}
		return false
	}
	return len(s) >= 7 && len(s) <= 15
}

func buildNetplanConfig(props map[string]string) string {
	ip := props["guestinfo.static_ip"]
	if ip == "" || !validIP(ip) {
		return ""
	}
	mask := props["guestinfo.netmask"]
	if mask == "" {
		mask = "255.255.255.0"
	}
	cidr := toCIDR(ip, mask)
	gw := props["guestinfo.gateway"]
	if gw != "" && !validIP(gw) {
		gw = ""
	}
	dns := props["guestinfo.dns"]
	if dns != "" && !validIP(dns) {
		dns = ""
	}

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

func buildUserdata(gatewayURL, key, hostname, defaultConfig, configPath string, ovfProps map[string]string, models []string) string {
	base := strings.TrimRight(gatewayURL, "/")
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", hostname)
	}

	// OS user/password/SSH from OVF vApp properties — must come before write_files.
	if ovfProps != nil {
		pw := ovfProps["guestinfo.password"]
		user := ovfProps["guestinfo.run_as_user"]
		sshKey := ovfProps["public-keys"]

		if pw != "" {
			fmt.Fprintf(&b, "password: %s\n", pw)
			b.WriteString("ssh_pwauth: true\n")
			b.WriteString("chpasswd:\n")
			b.WriteString("  expire: false\n")
			b.WriteString("  list:\n")
			fmt.Fprintf(&b, "    - ubuntu:%s\n", pw)
			if user != "" {
				fmt.Fprintf(&b, "    - %s:%s\n", user, pw)
			}
		}
		if user != "" {
			b.WriteString("users:\n")
			fmt.Fprintf(&b, "  - name: %s\n", user)
			b.WriteString("    lock_passwd: false\n")
			b.WriteString("    shell: /bin/bash\n")
			b.WriteString("    sudo: ALL=(ALL) ALL\n")
			if sshKey != "" {
				b.WriteString("    ssh_authorized_keys:\n")
				b.WriteString("      - " + sshKey + "\n")
			}
		} else if sshKey != "" {
			b.WriteString("ssh_authorized_keys:\n")
			b.WriteString("  - " + sshKey + "\n")
		}
	}

	// Ensure OpenCode config directory exists (cloud-init write_files does not create parents).
	b.WriteString("bootcmd:\n")
	b.WriteString("  - mkdir -p /home/vmware/.config/opencode\n")
	b.WriteString("  - chown -R vmware:vmware /home/vmware/.config\n")

	// All write_files entries must come after the write_files: header.
	b.WriteString("write_files:\n")

	// Build model list fragments from key models (operator-curated per agent).
	ocModelsJSON := ""
	ocCodeModelsJSON := ""
	if len(models) > 0 {
		type ocModel struct{ ID, Name string }
		ocList := make([]ocModel, len(models))
		ocCodeMap := make(map[string]map[string]string, len(models))
		for i, m := range models {
			ocList[i] = ocModel{ID: m, Name: m}
			ocCodeMap[m] = map[string]string{"name": m}
		}
		if b, err := json.Marshal(ocList); err == nil {
			ocModelsJSON = string(b)
		}
		if b, err := json.Marshal(ocCodeMap); err == nil {
			ocCodeModelsJSON = strings.TrimPrefix(strings.TrimSuffix(string(b), "}"), "{")
		}
	}

	// OpenClaw config with API key injected
	if key != "" {
		ocGatewayToken := GenerateGatewayToken()
		b.WriteString("  - path: /home/vmware/.openclaw/openclaw.json\n")
		b.WriteString("    owner: vmware:vmware\n")
		b.WriteString("    permissions: \"0600\"\n")
		b.WriteString("    content: |\n")
		fmt.Fprintf(&b, "      {\"gateway\":{\"auth\":{\"mode\":\"token\",\"token\":\"%s\"},\"bind\":\"lan\",\"mode\":\"local\",\"port\":18789},\"models\":{\"mode\":\"merge\",\"providers\":{\"litellm\":{\"api\":\"openai-completions\",\"apiKey\":\"%s\",\"baseUrl\":\"%s/v1\",\"models\":[%s]}}}}\n", ocGatewayToken, key, base, ocModelsJSON)
	}

	// OpenCode config — always written alongside OpenClaw for dual-mode templates.
	if key != "" && len(models) > 0 {
		b.WriteString("  - path: /home/vmware/.config/opencode/opencode.json\n")
		b.WriteString("    owner: vmware:vmware\n")
		b.WriteString("    permissions: \"0600\"\n")
		b.WriteString("    content: |\n")
		fmt.Fprintf(&b, "      {\"model\":\"openai/%s\",\"provider\":{\"openai\":{\"options\":{\"baseURL\":\"%s/v1\",\"apiKey\":\"%s\"},\"models\":{%s}}}}\n",
			models[0], base, key, ocCodeModelsJSON)
	}

	// No netplan needed — CustomizationSpec handles IP natively (vCenter/VMware Tools).
	// Cloud-init netplan would conflict with vCenter guest customization.
	if false {
		ip := ovfProps["guestinfo.static_ip"]
		mask := ovfProps["guestinfo.netmask"]
		if mask == "" {
			mask = "255.255.255.0"
		}
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
			if b&(1<<uint(i)) != 0 {
				ones++
			}
		}
	}
	return fmt.Sprintf("%s/%d", ip, ones)
}

func buildMetadata(hostname string) string {
	return fmt.Sprintf("local-hostname: %s\n", hostname)
}

// AgentMgrGuestInfo carries protocol fields for instant clone guest config.
type AgentMgrGuestInfo struct {
	Role, DeploymentID, Generation, Command string
	Hostname, InterfaceMAC, IPAddress, PrefixLength, Gateway, DNS, SearchDomain string
	OpenClawUser, OpenClawHome, OpenClawPort, OpenClawBaseURL, OpenClawModel string
	OpenClawAPIKey, OpenClawGatewayToken string // sensitive
}

// ToGuestInfo maps fields to bare keys for SetGuestinfo (prefix added by caller).
func (g *AgentMgrGuestInfo) ToGuestInfo() map[string]string {
	m := map[string]string{
		"agentmgr.role": g.Role, "agentmgr.deployment-id": g.DeploymentID,
		"agentmgr.generation": g.Generation, "agentmgr.command": g.Command,
		"agentmgr.hostname": g.Hostname, "agentmgr.interface-mac": g.InterfaceMAC,
		"agentmgr.ip-address": g.IPAddress, "agentmgr.prefix-length": g.PrefixLength,
		"agentmgr.gateway": g.Gateway, "agentmgr.dns": g.DNS,
		"agentmgr.search-domain": g.SearchDomain,
		"agentmgr.openclaw-user": g.OpenClawUser, "agentmgr.openclaw-home": g.OpenClawHome,
		"agentmgr.openclaw-port": g.OpenClawPort, "agentmgr.openclaw-base-url": g.OpenClawBaseURL,
		"agentmgr.openclaw-model": g.OpenClawModel,
		"agentmgr.openclaw-api-key": g.OpenClawAPIKey,
		"agentmgr.openclaw-gateway-token": g.OpenClawGatewayToken,
	}
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// GenerateGatewayToken creates a unique per-instance OpenClaw gateway auth token.
func GenerateGatewayToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("oc-gw-%x", time.Now().UnixNano())
	}
	return "oc-gw-" + hex.EncodeToString(b)
}
