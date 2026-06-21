package graph

import (
	"testing"

	"github.com/VMware-AI/agent-platform-backend/ent/migrate"
)

// tenantedTables is the known inventory of tables carrying a tenant_id column.
// It is the driver for the multi-tenant read sweep + cross-tenant isolation
// tests (LLD-10 §3.2): when a NEW entity with tenant_id is added, this test goes
// red, forcing the author to wire its tenant scoping (and register its
// cross-tenant test) rather than silently shipping an unisolated entity.
var tenantedTables = map[string]bool{
	"users":               true,
	"agents":              true,
	"agent_configs":       true,
	"artifacts":           true,
	"resource_pools":      true, // platform-shared per LLD-10, but still carries tenant_id
	"rate_limit_policies": true,
	"roles":               true,
	"departments":         true,
	"token_usages":        true,
	"agent_enrollments":   true, // LLD-08
	"rotation_commands":   true, // LLD-08
	"environments":        true, // LLD-10 B4 (tenant_id required; env is tenant-owned)
}

// TestTenantedEntityInventory reflects over ent's compiled schema and asserts the
// set of tables with a tenant_id column exactly matches the known inventory.
func TestTenantedEntityInventory(t *testing.T) {
	got := map[string]bool{}
	for _, tbl := range migrate.Tables {
		for _, c := range tbl.Columns {
			if c.Name == "tenant_id" {
				got[tbl.Name] = true
			}
		}
	}
	unregistered, stale := tenantedInventoryDiff(got, tenantedTables)
	for _, name := range unregistered {
		t.Errorf("table %q has a tenant_id column but is not in tenantedTables; "+
			"wire its tenant scoping + cross-tenant isolation test (LLD-10 §3.2)", name)
	}
	for _, name := range stale {
		t.Errorf("table %q is in tenantedTables but no longer has a tenant_id column", name)
	}
}

// tenantedInventoryDiff returns tables that carry tenant_id but are unregistered,
// and registry entries that no longer carry tenant_id.
func tenantedInventoryDiff(got, registry map[string]bool) (unregistered, stale []string) {
	for name := range got {
		if !registry[name] {
			unregistered = append(unregistered, name)
		}
	}
	for name := range registry {
		if !got[name] {
			stale = append(stale, name)
		}
	}
	return unregistered, stale
}

// TestTenantedInventory_DetectsUnregistered proves the guard CATCHES a new
// tenant_id entity that wasn't registered (AC-10).
func TestTenantedInventory_DetectsUnregistered(t *testing.T) {
	got := map[string]bool{"users": true, "sneaky_new_tenanted_table": true}
	registry := map[string]bool{"users": true}
	unregistered, _ := tenantedInventoryDiff(got, registry)
	if len(unregistered) != 1 || unregistered[0] != "sneaky_new_tenanted_table" {
		t.Fatalf("guard must flag the unregistered tenanted table, got %v", unregistered)
	}
}
