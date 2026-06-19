// Package deploy orchestrates agent-VM provisioning: it issues a per-user
// gateway key, builds the cloud-init payload, and injects it into the target VM
// via vCenter guestinfo. This is the vertical that connects the gateway and
// vcenter clients. See LLD-05 §3.
package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
)

// GuestinfoSetter is the slice of the vCenter client this package needs
// (satisfied by *vcenter.Client). Kept narrow for testability.
type GuestinfoSetter interface {
	SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error
}

// Service provisions agent VMs by wiring the gateway + vCenter together.
type Service struct {
	Gateway    gateway.Client
	VCenter    GuestinfoSetter
	GatewayURL string // LLM gateway base URL injected into the VM (e.g. https://gateway.internal)
}

// Request describes a single agent-VM provisioning. The VM is assumed already
// cloned from the OVA template; this step injects its per-VM config.
type Request struct {
	AgentName string
	UserID    string
	TeamID    string
	VMName    string // target VM in vCenter
	Hostname  string
	Models    []string
	MaxBudget *float64
}

// Result carries the issued secret (returned once) and the rendered userdata.
type Result struct {
	VirtualKey string // secret — surface once, never persist in plaintext
	Userdata   string
}

// Provision issues a key, renders cloud-init, and injects it via guestinfo.
func (s *Service) Provision(ctx context.Context, req Request) (*Result, error) {
	if s.Gateway == nil || s.VCenter == nil {
		return nil, fmt.Errorf("deploy: gateway and vcenter must be configured")
	}
	if req.VMName == "" || req.UserID == "" {
		return nil, fmt.Errorf("deploy: vmName and userId are required")
	}
	models := req.Models
	if len(models) == 0 {
		models = []string{"smart"} // difficulty router by default (LLD-04)
	}

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

	userdata := buildUserdata(s.GatewayURL, key.Key, req.Hostname)
	gi := map[string]string{"userdata": userdata}
	if req.Hostname != "" {
		gi["metadata"] = buildMetadata(req.Hostname)
	}
	if err := s.VCenter.SetGuestinfo(ctx, req.VMName, gi); err != nil {
		// Orphan prevention: the key was issued at the gateway but the VM was
		// never configured — revoke it so it does not linger (parallel to the
		// orphan-VM remediation).
		if delErr := s.Gateway.DeleteKey(ctx, key.Key); delErr != nil {
			return nil, fmt.Errorf("deploy: inject guestinfo: %w (key revoke also failed: %v)", err, delErr)
		}
		return nil, fmt.Errorf("deploy: inject guestinfo failed, issued key revoked: %w", err)
	}
	return &Result{VirtualKey: key.Key, Userdata: userdata}, nil
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
