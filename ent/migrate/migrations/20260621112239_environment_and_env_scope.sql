-- Modify "agent_configs" table
ALTER TABLE "agent_configs" ADD COLUMN "environment_id" uuid NULL;
-- Modify "agents" table
ALTER TABLE "agents" ADD COLUMN "environment_id" uuid NULL;
-- Modify "artifacts" table
ALTER TABLE "artifacts" ADD COLUMN "environment_id" uuid NULL;
-- Modify "rate_limit_policies" table
ALTER TABLE "rate_limit_policies" ADD COLUMN "environment_id" uuid NULL;
-- Modify "resource_pools" table
ALTER TABLE "resource_pools" ADD COLUMN "environment_id" uuid NULL;
-- Modify "token_usages" table
ALTER TABLE "token_usages" ADD COLUMN "environment_id" uuid NULL;
-- Create "environments" table
CREATE TABLE "environments" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tenant_id" uuid NOT NULL, "name" character varying NOT NULL, "description" character varying NULL, PRIMARY KEY ("id"));
-- Create index "environment_tenant_id" to table: "environments"
CREATE INDEX "environment_tenant_id" ON "environments" ("tenant_id");
-- Create index "environment_tenant_id_name" to table: "environments"
CREATE UNIQUE INDEX "environment_tenant_id_name" ON "environments" ("tenant_id", "name");
