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
	// VCenterInsecure skips vCenter TLS verification. Default false (verify on);
	// opt in only for air-gapped vCenters with a pinned/self-signed internal CA.
	VCenterInsecure bool
	// DBAutoMigrate runs ent auto-migration on startup. Default on for dev, OFF
	// for prod — prod must use reviewed versioned migrations, never auto-alter
	// the live schema on boot.
	DBAutoMigrate bool
	// AllowedOrigins is the CSRF Origin/Referer allowlist for state-changing
	// requests (ALLOWED_ORIGINS, comma-separated). Same-origin is always allowed.
	AllowedOrigins []string
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
	c.VCenterInsecure = getenv("VCENTER_INSECURE", "false") == "true"
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
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
