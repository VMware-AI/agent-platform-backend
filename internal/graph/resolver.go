package graph

import (
	"context"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// VCenterClient is what resolvers need from a connected vCenter (deploy +
// inventory). *vcenter.Client satisfies it; a fake satisfies it in tests.
type VCenterClient interface {
	SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error
	Inventory(ctx context.Context) (vcenter.Inventory, error)
	Logout(ctx context.Context) error
}

// VCenterConnector dials a vCenter. Injectable so resolvers run against vcsim.
type VCenterConnector func(ctx context.Context, endpoint, user, pass string, insecure bool) (VCenterClient, error)

// Resolver is the GraphQL root resolver, holding shared dependencies.
type Resolver struct {
	Ent        *ent.Client
	Sessions   session.Store
	SessionTTL time.Duration
	// SecureCookies sets the Secure flag on the session cookie (true behind TLS).
	SecureCookies bool
	// Gateway governs LiteLLM virtual keys; nil if no gateway is configured.
	Gateway gateway.Client
	// GatewayModels manages the model pool + difficulty router (nil disables sync).
	GatewayModels gateway.ModelManager
	// Secrets resolves resource-pool credentials (Vaultwarden); nil disables deploy.
	Secrets secrets.Resolver
	// GatewayURL is the LLM gateway base URL injected into provisioned VMs.
	GatewayURL string
	// VCenterConnect dials vCenter; nil disables deploy.
	VCenterConnect VCenterConnector
}
