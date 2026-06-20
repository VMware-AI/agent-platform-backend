package config

import "testing"

// Guards the safety-critical defaults: prod must never auto-migrate the schema
// (H3), and vCenter TLS verification must be on unless explicitly disabled (H-2).
func TestLoad_SafeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("REDIS_URL", "")

	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_AUTO_MIGRATE", "")
	t.Setenv("VCENTER_INSECURE", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.DBAutoMigrate {
		t.Error("prod must default to NO auto-migrate (versioned migrations only)")
	}
	if c.VCenterInsecure {
		t.Error("vCenter TLS verification must default ON (insecure=false)")
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
