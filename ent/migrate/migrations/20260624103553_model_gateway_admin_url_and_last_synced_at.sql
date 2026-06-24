-- Modify "gateway_connections" table
ALTER TABLE "gateway_connections" ADD COLUMN "admin_url" character varying NULL, ADD COLUMN "last_synced_at" timestamptz NULL;
