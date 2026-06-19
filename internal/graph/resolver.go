package graph

import (
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
)

// Resolver is the GraphQL root resolver, holding shared dependencies.
type Resolver struct {
	Ent        *ent.Client
	Sessions   session.Store
	SessionTTL time.Duration
	// SecureCookies sets the Secure flag on the session cookie (true behind TLS).
	SecureCookies bool
}
