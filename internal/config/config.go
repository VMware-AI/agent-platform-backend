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
	// ReconcileInterval is how often (seconds) the gateway-key reconciler runs.
	// 0 (default) disables it. Requires a configured gateway to have any effect.
	ReconcileInterval int
	// ReconcilePrune lets the reconciler heal drift (delete gateway orphans +
	// revoke stale rows). Default false = report-only ("对账"), the safe default.
	ReconcilePrune bool
	// AgentPkgBaseURL is the offline mirror base for agent install packages,
	// substituted for {{AGENT_PKG_BASE_URL}} in catalog install commands. Empty
	// leaves the placeholder intact (operator must configure the mirror).
	AgentPkgBaseURL string
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
	ri, err := strconv.Atoi(getenv("RECONCILE_INTERVAL_SECONDS", "0"))
	if err != nil {
		return nil, fmt.Errorf("RECONCILE_INTERVAL_SECONDS must be an integer: %w", err)
	}
	if ri < 0 {
		return nil, fmt.Errorf("RECONCILE_INTERVAL_SECONDS must be >= 0, got %d", ri)
	}
	c.ReconcileInterval = ri
	c.ReconcilePrune = getenv("RECONCILE_PRUNE", "false") == "true"
	c.AgentPkgBaseURL = strings.TrimRight(os.Getenv("AGENT_PKG_BASE_URL"), "/")
	c.EnvScopeEnabled = getenv("ENV_SCOPE_ENABLED", "false") == "true"
	pcttl, err := strconv.Atoi(getenv("PERM_CACHE_TTL_SECONDS", "0"))
	if err != nil {
		return nil, fmt.Errorf("PERM_CACHE_TTL_SECONDS must be an integer: %w", err)
	}
	if pcttl < 0 {
		return nil, fmt.Errorf("PERM_CACHE_TTL_SECONDS must be >= 0, got %d", pcttl)
	}
	c.PermCacheTTLSeconds = pcttl
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
