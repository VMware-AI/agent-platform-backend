-- Modify "resource_pools" table
ALTER TABLE "resource_pools" DROP COLUMN "datacenter_count", DROP COLUMN "cluster_count", DROP COLUMN "host_count", DROP COLUMN "vm_count", ADD COLUMN "inventory" jsonb NULL;
