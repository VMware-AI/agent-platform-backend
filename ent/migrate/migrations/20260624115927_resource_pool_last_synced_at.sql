-- Modify "resource_pools" table
ALTER TABLE "resource_pools" ADD COLUMN "last_synced_at" timestamptz NULL;
