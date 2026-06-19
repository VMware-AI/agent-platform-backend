package graph

import (
	"context"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/deploy"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// VCenterConnector dials a vCenter and returns a guestinfo-capable client.
// Injectable so deployAgent can run against vcsim in tests.
type VCenterConnector func(ctx context.Context, endpoint, user, pass string, insecure bool) (deploy.GuestinfoSetter, error)

// Resolver is the GraphQL root resolver, holding shared dependencies.
type Resolver struct {
	Ent        *ent.Client
	Sessions   session.Store
	SessionTTL time.Duration
	// SecureCookies sets the Secure flag on the session cookie (true behind TLS).
	SecureCookies bool
	// Gateway governs LiteLLM virtual keys; nil if no gateway is configured.
	Gateway gateway.Client
	// Secrets resolves resource-pool credentials (Vaultwarden); nil disables deploy.
	Secrets secrets.Resolver
	// GatewayURL is the LLM gateway base URL injected into provisioned VMs.
	GatewayURL string
	// VCenterConnect dials vCenter; nil disables deploy.
	VCenterConnect VCenterConnector
}
