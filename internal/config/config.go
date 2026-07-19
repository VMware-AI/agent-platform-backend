// Package config loads and validates runtime configuration at startup (fail-fast).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds backend runtime settings. All values come from the environment
// (12-factor); secrets must never be hardcoded.
type Config struct {
	HTTPAddr    string // e.g. ":8080"
	DatabaseURL string // postgres://...  (empty => sqlite in-memory for dev/test)
	RedisURL    string // redis://...     (empty => in-memory session store)
	SessionTTL  int    // seconds
	Env         string // dev | prod
	// DBAutoMigrate runs ent auto-migration on startup. Default on for dev, OFF
	// for prod — prod must use reviewed versioned migrations, never auto-alter
	// the live schema on boot.
	DBAutoMigrate bool
	// AllowedOrigins is the CSRF Origin/Referer allowlist for state-changing
	// requests (ALLOWED_ORIGINS, comma-separated). Same-origin is always allowed.
	AllowedOrigins []string
	// LitellmReconcileInterval is how often (seconds) the unified
	// DB↔LiteLLM reconciler runs. Default 900 (15m). Set 0 to disable.
	// Driven by the LiteLLM design doc §3.2 "DB→LiteLLM 周期对账".
	LitellmReconcileInterval int
	// AgentPkgBaseURL is the offline mirror base for agent install packages:
	// substituted for {{AGENT_PKG_BASE_URL}} in catalog install commands, and
	// stamped into agent VMs as guestinfo.agentmgr.agent_pkg_base_url so the
	// in-VM webadmin can pull upgrade packages. May embed read-only mirror
	// credentials (ftp://user:pass@...) — never log it. Empty leaves the
	// placeholder intact and stamps nothing (operator must configure the mirror).
	AgentPkgBaseURL string
	// AgentKeepVersions is stamped into agent VMs as
	// guestinfo.agentmgr.agent_keep_versions at deploy: how many installed agent
	// versions the VM's upgrader retains when pruning. 0 (the default) is the
	// "unset" sentinel — nothing is stamped and the daemon default (3) applies;
	// the daemon rejects values < 1, so "retain nothing" is not expressible.
	AgentKeepVersions int
	// EnvScopeEnabled turns on environment (env_scope) filtering on top of tenant
	// isolation (LLD-10 §2.3). OFF by default — the tables/columns exist but env
	// filtering only activates once the frontend X-Environment contract is ready.
	EnvScopeEnabled bool
	// PermCacheTTLSeconds enables the in-process @hasPermission cache with this TTL.
	// 0 (default) DISABLES it: the cache is process-local, so role/permission
	// revocation does not propagate across replicas (a revoked permission stays
	// honored on other replicas until TTL). Safe only for single-replica deployments
	// until a shared (Redis pub/sub) invalidation channel lands; set >0 to opt in.
	PermCacheTTLSeconds int
	// DBMaxOpenConns bounds the postgres pool's total open connections. Go's
	// default is 0 = UNLIMITED, which lets concurrent load open connections without
	// ceiling and exhaust Postgres max_connections (worse across replicas), so we
	// default to a finite value. 0 restores the unlimited default. Ignored for the
	// dev sqlite path.
	DBMaxOpenConns int
	// DBMaxIdleConns is the idle-connection ceiling kept warm in the pool (Go's
	// default of 2 causes connect/close churn under load).
	DBMaxIdleConns int
	// DBConnMaxLifetimeMinutes recycles a connection after this long (0 = never).
	// A finite lifetime plays well with failover and PgBouncer.
	DBConnMaxLifetimeMinutes int
	// PoolSyncIntervalSeconds is how often (seconds) the background goroutine
	// re-syncs every resource pool that has stored credentials.
	// Default 3600 (60m); set 0 to disable. The first sync also runs
	// fire-and-forget at resource-pool creation time, so even with 0 the
	// operator can trigger a sync via the syncResourcePool mutation.
	PoolSyncIntervalSeconds int
	// PoolSyncTimeoutSeconds caps a single pool's sync (connect + inventory
	// + full inventory + DB write). Default 30s; reduce for high-fanout
	// fleets where one slow vCenter would otherwise block ticker progress
	// (the per-endpoint circuit breaker provides the heavy lifting; this is
	// just the time budget).
	PoolSyncTimeoutSeconds int
	// PoolSyncMaxRetries is the number of retries *on top of* the first
	// attempt, with exponential backoff (1s/2s/4s + jitter). Only
	// network/timeout/5xx-style errors retry; business errors (auth
	// failure, object-not-found) return immediately. Default 3.
	PoolSyncMaxRetries int
	// PoolSyncBreakerThreshold trips the per-endpoint circuit breaker after
	// this many consecutive sync failures. Default 5. Zero or negative
	// disables the breaker (falls back to no-op settings).
	PoolSyncBreakerThreshold int
	// PoolSyncBreakerOpenSeconds keeps the breaker in Open state for this
	// long after tripping; after which it enters HalfOpen and lets one
	// request through to probe. Default 60s.
	PoolSyncBreakerOpenSeconds int
	// SpendCacheTTLSeconds memoizes observability spend reports across gateways
	// (LLD-15). Default 30s; 0 disables (each request fans out fresh).
	SpendCacheTTLSeconds int
	// ModelGatewaySyncIntervalSeconds is how often (seconds) the background
	// goroutine syncs every litellm GatewayConnection row (status, routing
	// strategy, backend-model count). Default 1800 (30m). 0 disables the
	// periodic sync — operators can still trigger a sync manually via the
	// syncModelGatewayConnection mutation, and a sync is also fired-and-
	// forgotten at create time.
	ModelGatewaySyncIntervalSeconds int
	// ProviderProbeIntervalSeconds is how often (seconds) the background
	// goroutine probes each enabled ProviderModel against its upstream API
	// and writes the resulting Active/Degraded/Melted/Unknown status back to
	// the row. Default 60 (1m); 0 disables the periodic probe. Upserts
	// also kick off a one-shot probe so the operator sees the real status
	// within seconds rather than waiting for the next tick.
	// Driven by the LiteLLM design doc §2.2 "状态异步探测解耦机制" — the
	// frontend only reads the cached status, never blocks on a live probe.
	ProviderProbeIntervalSeconds int
	// SecretsEncryptionKey is the single symmetric key (any high-entropy string;
	// SHA-256-derived to 32 bytes) used to encrypt credential fields at rest in the
	// platform_secrets table. The same key is used in every environment — there is
	// no dev/prod secrets split. Required: the server refuses to start without it
	// (an empty key would mean credentials cannot be sealed/opened). For rotation,
	// prefer SecretsEncryptionKeys below — SecretsEncryptionKey still works and is
	// equivalent to a single-key map tagged with key id "default".
	SecretsEncryptionKey string
	// SecretsEncryptionKeys (optional) is the rotation-friendly form of
	// SecretsEncryptionKey: "id1:passphrase1,id2:passphrase2". The first id
	// is the active key (new Put writes go under it); every listed key is
	// available for Resolve so a row sealed under an older key keeps
	// decrypting until the rotation worker migrates it. When this is set it
	// takes precedence over SecretsEncryptionKey. Empty values in the list
	// are skipped; ids must be unique and non-empty.
	SecretsEncryptionKeys string
	// SecretsRotationIntervalSeconds is how often (seconds) the background
	// goroutine scans platform_secrets for rows still encrypted under a
	// retired key and re-encrypts them under the active key. Default 0
	// (disabled) — the worker is opt-in (rotation in production is a
	// deliberate operator action; we don't surprise them by migrating
	// existing ciphertexts under their nose). Set to N > 0 to enable.
	SecretsRotationIntervalSeconds int
	// SecretsAuditEnabled controls whether every successful Secrets.Resolve
	// call is recorded as an audit-log row (action="secret.read"). Default
	// false — the worker is opt-in because a busy deploy (5-min router sync
	// + 60s probes × N upstreams + pool sync) easily writes hundreds of
	// audit rows per hour. Operators investigating an incident enable it
	// for the duration of the investigation.
	SecretsAuditEnabled bool
}

// Load reads config from the environment and validates it. Fails fast on a
// malformed value rather than starting in a broken state.
func Load() (*Config, error) {
	c := &Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		Env:         getenv("APP_ENV", "dev"),
	}
	ttl, err := strconv.Atoi(getenv("SESSION_TTL_SECONDS", "28800")) // 8h
	if err != nil {
		return nil, fmt.Errorf("SESSION_TTL_SECONDS must be an integer: %w", err)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("SESSION_TTL_SECONDS must be positive, got %d", ttl)
	}
	c.SessionTTL = ttl
	if c.Env != "dev" && c.Env != "prod" {
		return nil, fmt.Errorf("APP_ENV must be dev|prod, got %q", c.Env)
	}
	defAutoMigrate := "false"
	if c.Env == "dev" {
		defAutoMigrate = "true"
	}
	c.DBAutoMigrate = getenv("DB_AUTO_MIGRATE", defAutoMigrate) == "true"
	for _, o := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	ri, err := strconv.Atoi(getenv("LITELLM_RECONCILE_INTERVAL_SECONDS", "900"))
	if err != nil {
		return nil, fmt.Errorf("LITELLM_RECONCILE_INTERVAL_SECONDS must be an integer: %w", err)
	}
	if ri < 0 {
		return nil, fmt.Errorf("LITELLM_RECONCILE_INTERVAL_SECONDS must be >= 0, got %d", ri)
	}
	c.LitellmReconcileInterval = ri
	c.AgentPkgBaseURL = strings.TrimRight(os.Getenv("AGENT_PKG_BASE_URL"), "/")
	if c.AgentKeepVersions, err = getenvInt("AGENT_KEEP_VERSIONS", 0); err != nil {
		return nil, err
	}
	c.EnvScopeEnabled = getenv("ENV_SCOPE_ENABLED", "false") == "true"
	pcttl, err := strconv.Atoi(getenv("PERM_CACHE_TTL_SECONDS", "0"))
	if err != nil {
		return nil, fmt.Errorf("PERM_CACHE_TTL_SECONDS must be an integer: %w", err)
	}
	if pcttl < 0 {
		return nil, fmt.Errorf("PERM_CACHE_TTL_SECONDS must be >= 0, got %d", pcttl)
	}
	c.PermCacheTTLSeconds = pcttl
	if c.DBMaxOpenConns, err = getenvInt("DB_MAX_OPEN_CONNS", 20); err != nil {
		return nil, err
	}
	if c.DBMaxIdleConns, err = getenvInt("DB_MAX_IDLE_CONNS", 10); err != nil {
		return nil, err
	}
	if c.DBConnMaxLifetimeMinutes, err = getenvInt("DB_CONN_MAX_LIFETIME_MINUTES", 30); err != nil {
		return nil, err
	}
	if c.PoolSyncIntervalSeconds, err = getenvInt("POOL_SYNC_INTERVAL_SECONDS", 3600); err != nil {
		return nil, err
	}
	if c.PoolSyncTimeoutSeconds, err = getenvInt("POOL_SYNC_TIMEOUT_SECONDS", 30); err != nil {
		return nil, err
	}
	if c.PoolSyncMaxRetries, err = getenvInt("POOL_SYNC_MAX_RETRIES", 3); err != nil {
		return nil, err
	}
	if c.PoolSyncBreakerThreshold, err = getenvInt("POOL_SYNC_BREAKER_THRESHOLD", 5); err != nil {
		return nil, err
	}
	if c.SpendCacheTTLSeconds, err = getenvInt("OBS_SPEND_CACHE_TTL_SECONDS", 30); err != nil {
		return nil, err
	}
	if c.PoolSyncBreakerOpenSeconds, err = getenvInt("POOL_SYNC_BREAKER_OPEN_SECONDS", 60); err != nil {
		return nil, err
	}
	if c.ProviderProbeIntervalSeconds, err = getenvInt("PROVIDER_PROBE_INTERVAL_SECONDS", 600); err != nil {
		return nil, err
	}
	c.SecretsEncryptionKey = os.Getenv("SECRETS_ENCRYPTION_KEY")
	c.SecretsEncryptionKeys = os.Getenv("SECRETS_ENCRYPTION_KEYS")
	c.SecretsAuditEnabled = getenv("SECRETS_AUDIT_ENABLED", "false") == "true"
	if c.SecretsRotationIntervalSeconds, err = getenvInt("SECRETS_ROTATION_INTERVAL_SECONDS", 0); err != nil {
		return nil, err
	}
	if c.SecretsRotationIntervalSeconds < 0 {
		return nil, fmt.Errorf("SECRETS_ROTATION_INTERVAL_SECONDS must be >= 0, got %d", c.SecretsRotationIntervalSeconds)
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvInt parses a non-negative integer env var, falling back to def when
// unset. A malformed or negative value is a fail-fast startup error.
func getenvInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be >= 0, got %d", key, n)
	}
	return n, nil
}
