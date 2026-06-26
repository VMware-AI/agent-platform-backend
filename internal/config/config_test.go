package config

import "testing"

// Guards the safety-critical default: prod must never auto-migrate the schema
// (H3). vCenter TLS verification is no longer a global env — it's a per-pool
// ResourcePool.insecure field (default false / verify on), see LLD-13.
func TestLoad_SafeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")

	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_AUTO_MIGRATE", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.DBAutoMigrate {
		t.Error("prod must default to NO auto-migrate (versioned migrations only)")
	}

	t.Setenv("APP_ENV", "dev")
	c, _ = Load()
	if !c.DBAutoMigrate {
		t.Error("dev must default to auto-migrate (in-memory/local convenience)")
	}

	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_AUTO_MIGRATE", "true")
	c, _ = Load()
	if !c.DBAutoMigrate {
		t.Error("explicit DB_AUTO_MIGRATE=true must enable auto-migrate")
	}
}

// The gateway-key reconciler is off and report-only by default — the safe stance.
func TestLoad_ReconcileDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("APP_ENV", "dev")
	t.Setenv("RECONCILE_INTERVAL_SECONDS", "")
	t.Setenv("RECONCILE_PRUNE", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ReconcileInterval != 0 {
		t.Errorf("reconciler must default OFF, got interval=%d", c.ReconcileInterval)
	}
	if c.ReconcilePrune {
		t.Error("reconciler must default to report-only (prune=false)")
	}

	t.Setenv("RECONCILE_INTERVAL_SECONDS", "-5")
	if _, err := Load(); err == nil {
		t.Error("negative interval must fail validation")
	}

	t.Setenv("RECONCILE_INTERVAL_SECONDS", "300")
	t.Setenv("RECONCILE_PRUNE", "true")
	c, _ = Load()
	if c.ReconcileInterval != 300 || !c.ReconcilePrune {
		t.Errorf("explicit config not applied: interval=%d prune=%v", c.ReconcileInterval, c.ReconcilePrune)
	}
}
