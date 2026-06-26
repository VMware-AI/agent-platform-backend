-- Modify "resource_pools" table
ALTER TABLE "resource_pools" ADD COLUMN "insecure" boolean NOT NULL DEFAULT false;
