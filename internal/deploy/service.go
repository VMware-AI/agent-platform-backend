// Package deploy orchestrates agent-VM provisioning: it issues a per-user
// gateway key, builds the cloud-init payload, and injects it into the target VM
// via vCenter guestinfo. This is the vertical that connects the gateway and
// vcenter clients. See LLD-05 §3.
package deploy

import (
	"context"
	"fmt"
	"log"
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
	Hostname     string
	Models       []string
	MaxBudget    *float64
}

// Result carries the issued secret (returned once), the rendered userdata, and
// the created VM name.
type Result struct {
	VirtualKey string // secret — surface once, never persist in plaintext
	Userdata   string
	VMName     string
}

// Provision issues a key, clones the agent VM from the OVA template, injects
// cloud-init via guestinfo, and powers it on. On any step's failure it rolls
// back the work already done so no orphan VM or gateway key is left (LLD-05 §3).
func (s *Service) Provision(ctx context.Context, req Request) (*Result, error) {
	if s.Gateway == nil || s.VCenter == nil {
		return nil, fmt.Errorf("deploy: gateway and vcenter must be configured")
	}
	if req.VMName == "" || req.UserID == "" || req.Template == "" {
		return nil, fmt.Errorf("deploy: template, vmName and userId are required")
	}
	models := req.Models
	if len(models) == 0 {
		models = []string{"smart"} // difficulty router by default (LLD-04)
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

	// 2) Clone the agent VM from the OVA template, powered off so guestinfo is
	//    set before first boot.
	if _, err := s.VCenter.CloneFromTemplate(ctx, vcenter.CloneSpec{
		Template:     req.Template,
		Name:         req.VMName,
		ResourcePool: req.ResourcePool,
		PowerOn:      false,
	}); err != nil {
		s.revokeKey(ctx, key.Key) // no VM to clean up yet
		return nil, fmt.Errorf("deploy: clone template: %w", err)
	}

	// 3) Inject per-VM cloud-init via guestinfo.
	userdata := buildUserdata(s.GatewayURL, key.Key, req.Hostname)
	gi := map[string]string{"userdata": userdata}
	if req.Hostname != "" {
		gi["metadata"] = buildMetadata(req.Hostname)
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

	return &Result{VirtualKey: key.Key, Userdata: userdata, VMName: req.VMName}, nil
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

// buildUserdata renders the cloud-init that drops the gateway env (LLD-05 §3).
func buildUserdata(gatewayURL, key, hostname string) string {
	base := strings.TrimRight(gatewayURL, "/")
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", hostname)
	}
	b.WriteString("write_files:\n")
	b.WriteString("  - path: /etc/agent/llm-gateway.env\n")
	b.WriteString("    permissions: \"0640\"\n")
	b.WriteString("    owner: root:agent\n")
	b.WriteString("    content: |\n")
	fmt.Fprintf(&b, "      OPENAI_BASE_URL=%s/v1\n", base)
	fmt.Fprintf(&b, "      OPENAI_API_KEY=%s\n", key)
	return b.String()
}

func buildMetadata(hostname string) string {
	return fmt.Sprintf("local-hostname: %s\n", hostname)
}
