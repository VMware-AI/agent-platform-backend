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
	PowerOff(ctx context.Context, vmName string) error
	Destroy(ctx context.Context, vmName string) error
	Inventory(ctx context.Context) (vcenter.Inventory, error)
	// FullInventory walks the vCenter inventory tree (per-DC errgroup +
	// PBM) into the sync schema. Returned by FullInventory(ctx); the data
	// is consumed by the resource-pool datacenters GraphQL field and powers
	// the OVA deploy cascading dropdowns.
	FullInventory(ctx context.Context) ([]vcenter.DataCenter, error)
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
	ReconfigVM(ctx context.Context, vmName string, spec vcenter.ReconfigSpec) error
	GetVMHardware(ctx context.Context, vmName string) (*vcenter.VMHardware, error)
	GetVAppProperties(ctx context.Context, vmName string) ([]vcenter.OVFProperty, error)
	InstantClone(ctx context.Context, spec vcenter.InstantCloneSpec) (*vcenter.VMInfo, error)
	ListRunningVMs(ctx context.Context) ([]vcenter.VMInfo, error)
	RemoveSerialPorts(ctx context.Context, vmName string) error
	CheckInstantCloneCompatible(ctx context.Context, vmName string) error
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
	// GatewayKeyClientFor builds a litellm key/team client bound to a specific
	// gateway row, for per-department routing (LLD-13 §3.3). Injectable for tests;
	// nil → a real HTTP client (see Resolver.buildGatewayKeyClient).
	GatewayKeyClientFor func(ctx context.Context, g *ent.GatewayConnection) gateway.Client
	// SpendReaderFor builds a litellm spend/budget reader bound to a gateway row,
	// for the observability spend aggregator (LLD-15). Injectable for tests;
	// nil → a real HTTP client (see Resolver.buildSpendReader).
	SpendReaderFor func(ctx context.Context, g *ent.GatewayConnection) gateway.SpendReader
	// spendCache memoizes spendReport results across gateways for a short TTL so
	// page polling doesn't fan out to litellm on every request. nil = disabled
	// (default; tests want fresh reads). Enabled in main via EnableSpendCache.
	spendCache *spendReportCache
	// Secrets resolves resource-pool credentials (encrypted DBStore); nil disables deploy.
	Secrets secrets.Resolver
	// SecretsAuditEnabled, when true, causes every successful Secrets.Resolve to
	// write an audit_log row (action="secret.read"). Default false — opt-in via
	// the SECRETS_AUDIT_ENABLED env. The flag lives on Resolver so callers can
	// enable it without importing the secrets package.
	SecretsAuditEnabled bool
	// InstallVars holds the STATIC env-derived {{PLACEHOLDER}} tokens for
	// AgentTemplate.install_command (currently just AGENT_PKG_BASE_URL).
	// AGENT_USER is set on the resolver at startup (see AgentUser below).
	InstallVars map[string]string
	// AgentUser is the OS account installed agents run as, substituted into
	// {{AGENT_USER}} in catalog install commands. Set once at startup from
	// the AGENT_USER env (cmd/server); empty means "use defaultAgentUser".
	AgentUser string
	// VCenterConnect dials vCenter; nil disables deploy. TLS-skip is per-pool
	// (ResourcePool.insecure, LLD-13), passed into each connect call.
	VCenterConnect VCenterConnector
	// AgentMgr issues VM enrollments + processes heartbeats (LLD-08); nil disables
	// agent-manager (deploy then injects no enroll token).
	AgentMgr *agentmgr.Service
	// ControlPlaneURL is the backend base URL injected into VMs for the daemon's
	// heartbeat/enroll calls (LLD-08).
	ControlPlaneURL string
	// AgentPkgBaseURL is the internal artifact mirror base stamped into agent VMs
	// (guestinfo.agentmgr.agent_pkg_base_url) so the in-VM webadmin can pull
	// upgrade packages — same value as InstallVars' AGENT_PKG_BASE_URL. May embed
	// read-only mirror credentials; never log it.
	AgentPkgBaseURL string
	// AgentKeepVersions is stamped as guestinfo.agentmgr.agent_keep_versions at
	// deploy (0 = don't stamp; the daemon default of 3 applies).
	AgentKeepVersions int
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
	// Pool fault-tolerance chain (syncResourcePool + auto-sync + first sync).
	// nil disables all three sync paths; the rest of the resource-pool CRUD
	// (create / update / delete / test-connection / list) keeps working.
	poolBreakers       *poolBreakerRegistry
	poolSyncTimeout    time.Duration
	poolSyncMaxRetries int
	// poolSyncLocks serialises concurrent syncs of the SAME pool. The ticker, the
	// manual syncResourcePool mutation and the create-time first sync can all fire
	// for one pool at once; without this they would interleave their status +
	// inventory writes (last-writer-wins) and double the vCenter login load. Keyed
	// by pool id; a per-pool mutex is created lazily under poolSyncLocksMu.
	poolSyncLocks   map[uuid.UUID]*sync.Mutex
	poolSyncLocksMu sync.Mutex
	// lastRouterSettingsHash memoizes the SHA-256 hex of each gateway's last
	// successfully POSTed /config/update payload. Shared across the periodic
	// worker (StartRouterSettingsSync) and the resolver-side fire-and-forget
	// hook (AggregateAndPushRouterSettings) so a stable payload short-circuits
	// every push path. Process-local — multi-replica deployments accept the
	// redundant-but-correct first-tick push from each replica.
	lastRouterSettingsHash   map[uuid.UUID]string
	lastRouterSettingsHashMu sync.Mutex
}

// EnablePermissionCache turns on memoization of custom-role permission sets for
// the @hasPermission directive. Entries expire after ttl and are invalidated
// eagerly when roles change — but only on THIS replica (the cache is
// process-local), so enable it single-replica only (see permcache.go). Disabled
// by default; opt in via PERM_CACHE_TTL_SECONDS.
func (r *Resolver) EnablePermissionCache(ttl time.Duration) {
	r.permCache = newPermCache(ttl)
}

// EnablePoolSync wires the fault-tolerance chain (timeout + retry + breaker)
// for the resource-pool sync paths. Called by main after constructing the
// Resolver; tests that don't want sync behavior simply skip this call, in
// which case syncOnePool / CreateResourcePool's fire-and-forget first sync
// / the background ticker all become no-ops.
func (r *Resolver) EnablePoolSync(timeout time.Duration, maxRetries, threshold, openSec int) {
	// gobreaker.ReadyToTrip expects uint32; we accept int from config and
	// clamp negatives to 0. The registry treats 0 as "breaker disabled"
	// (ReadyToTrip never fires), matching the config doc and giving tests a
	// registered-but-passive breaker.
	var thr uint32
	if threshold > 0 {
		thr = uint32(threshold)
	}
	r.poolBreakers = newPoolBreakerRegistry(thr, openSec)
	r.poolSyncTimeout = timeout
	r.poolSyncMaxRetries = maxRetries
}
