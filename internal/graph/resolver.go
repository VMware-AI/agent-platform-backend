package graph

import (
	"context"
	"sync"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/agentmgr"
	"github.com/VMware-AI/agent-platform-backend/internal/gateway"
	"github.com/VMware-AI/agent-platform-backend/internal/ratelimit"
	"github.com/VMware-AI/agent-platform-backend/internal/secrets"
	"github.com/VMware-AI/agent-platform-backend/internal/session"
	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
	"github.com/google/uuid"
)

// VCenterClient is what resolvers need from a connected vCenter (deploy +
// inventory). *vcenter.Client satisfies it; a fake satisfies it in tests.
// It is a superset of deploy.VMProvisioner so a connected client drives the
// full provisioning lifecycle.
type VCenterClient interface {
	CloneFromTemplate(ctx context.Context, spec vcenter.CloneSpec) (*vcenter.VMInfo, error)
	DeployOVF(ctx context.Context, spec vcenter.OVFDeploySpec) (*vcenter.VMInfo, error)
	ListTemplates(ctx context.Context) ([]vcenter.VMInfo, error)
	ListResourcePools(ctx context.Context) ([]vcenter.ResourcePoolInfo, error)
	SetGuestinfo(ctx context.Context, vmName string, kv map[string]string) error
	PowerOn(ctx context.Context, vmName string) error
	Destroy(ctx context.Context, vmName string) error
	Inventory(ctx context.Context) (vcenter.Inventory, error)
	CreateSnapshot(ctx context.Context, vmName, name, description string) error
	RevertSnapshot(ctx context.Context, vmName, snapshotName string) error
	ListSnapshots(ctx context.Context, vmName string) ([]vcenter.SnapshotInfo, error)
	// ListContentLibraries returns names of all content libraries on the vCenter
	// (资源池接入表单 下拉选择).
	ListContentLibraries(ctx context.Context) ([]string, error)
	// ListContentLibraryItems returns OVF/OVA items in the named content library
	// (OVA 模板新增表单 下拉选择).
	ListContentLibraryItems(ctx context.Context, libraryName string) ([]vcenter.LibraryItem, error)
	// ListNetworks returns all standard portgroups and dvPortgroups in the vCenter
	// (deploy form NIC/portgroup picker).
	ListNetworks(ctx context.Context) ([]vcenter.NetworkInfo, error)
	// GetTemplateVAppProperties reads the vAppConfig from a deployed VM template
	// and returns its user-configurable OVF properties for the deploy form.
	GetTemplateVAppProperties(ctx context.Context, templateName string) ([]vcenter.OVFProperty, error)
	ListVMs(ctx context.Context) ([]vcenter.VMInfo, error)
	GetVMInfo(ctx context.Context, vmName string) (*vcenter.VMInfo, error)
	// About returns the vCenter version/build identity (test-connection detail).
	About() vcenter.AboutInfo
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
	// GatewayClientFor builds a litellm model-manager bound to a specific gateway
	// row (its own endpoint + master key) for model/connection-test ops. Injectable
	// for tests; nil → a real HTTP client (see Resolver.buildGatewayModels).
	GatewayClientFor func(ctx context.Context, endpoint, masterKey string) gateway.ModelManager
	// GatewayKeyClientFor builds a litellm key/team client bound to a specific
	// gateway row, for per-department routing (LLD-13 §3.3). Injectable for tests;
	// nil → a real HTTP client (see Resolver.buildGatewayKeyClient).
	GatewayKeyClientFor func(ctx context.Context, g *ent.GatewayConnection) gateway.Client
	// Secrets resolves resource-pool credentials (encrypted DBStore); nil disables deploy.
	Secrets secrets.Resolver
	// GatewayURL is the LLM gateway base URL injected into provisioned VMs.
	GatewayURL string
	// InstallVars holds the STATIC env-derived {{PLACEHOLDER}} tokens for
	// AgentTemplate.install_command (currently just AGENT_PKG_BASE_URL). AGENT_USER
	// is NOT here — it is a DB platform setting merged in per-request by
	// renderInstallVars (LLD-13).
	InstallVars map[string]string
	// VCenterConnect dials vCenter; nil disables deploy. TLS-skip is per-pool
	// (ResourcePool.insecure, LLD-13), passed into each connect call.
	VCenterConnect VCenterConnector
	// AgentMgr issues VM enrollments + processes heartbeats (LLD-08); nil disables
	// agent-manager (deploy then injects no enroll token).
	AgentMgr *agentmgr.Service
	// ControlPlaneURL is the backend base URL injected into VMs for the daemon's
	// heartbeat/enroll calls (LLD-08).
	ControlPlaneURL string
	// EnvScopeEnabled turns on env_scope soft filtering (LLD-10 §2.3); OFF by
	// default until the frontend X-Environment contract is live.
	EnvScopeEnabled bool
	// LoginLimiter throttles failed logins (brute-force defense); nil disables it.
	LoginLimiter ratelimit.Limiter
	// permCache memoizes custom-role permission sets for @hasPermission; nil
	// disables caching (every check queries — used in tests for freshness).
	permCache *permCache
	// inflightSyncs is a process-local set of GatewayConnection IDs that are
	// currently being synced. toModelGateway consults it to project the
	// "currently syncing" overlay onto lastSyncStatus, so a long sync shows
	// SYNCING in the list without a separate persisted column. Best-effort:
	// multi-replica deployments cannot see each other's in-flight writes, and
	// a crashed/panic'd sync leaves the entry until process restart (the
	// resolver only ever adds, never auto-expires). Periodic cleanup is via
	// the in-process list on each sync entry's defer, which is enough for
	// the UI's purposes (eventual consistency on crash).
	inflightSyncs   map[uuid.UUID]struct{}
	inflightSyncsMu sync.Mutex
}

// EnablePermissionCache turns on memoization of custom-role permission sets for
// the @hasPermission directive. Entries expire after ttl and are invalidated
// eagerly when roles change — but only on THIS replica (the cache is
// process-local), so enable it single-replica only (see permcache.go). Disabled
// by default; opt in via PERM_CACHE_TTL_SECONDS.
func (r *Resolver) EnablePermissionCache(ttl time.Duration) {
	r.permCache = newPermCache(ttl)
}
