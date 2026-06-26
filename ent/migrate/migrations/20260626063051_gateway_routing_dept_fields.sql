-- Modify "departments" table
ALTER TABLE "departments" ADD COLUMN "gateway_connection_id" uuid NULL;
-- Modify "gateway_connections" table
ALTER TABLE "gateway_connections" ADD COLUMN "public_url" character varying NULL, ADD COLUMN "is_default" boolean NOT NULL DEFAULT false;
