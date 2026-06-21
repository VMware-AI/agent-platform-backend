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
	for name := range got {
		if !tenantedTables[name] {
			t.Errorf("table %q has a tenant_id column but is not in tenantedTables; "+
				"wire its tenant scoping + cross-tenant isolation test (LLD-10 §3.2)", name)
		}
	}
	for name := range tenantedTables {
		if !got[name] {
			t.Errorf("table %q is in tenantedTables but no longer has a tenant_id column", name)
		}
	}
}
