-- Drop index "artifact_name_version" from table: "artifacts"
DROP INDEX "artifact_name_version";
-- Create index "artifact_name_version" to table: "artifacts"
CREATE UNIQUE INDEX "artifact_name_version" ON "artifacts" ("name", "version") WHERE (tenant_id IS NULL);
-- Create index "artifact_tenant_id_name_version" to table: "artifacts"
CREATE UNIQUE INDEX "artifact_tenant_id_name_version" ON "artifacts" ("tenant_id", "name", "version") WHERE (tenant_id IS NOT NULL);
-- Drop index "rate_limit_policies_name_key" from table: "rate_limit_policies"
DROP INDEX "rate_limit_policies_name_key";
-- Create index "ratelimitpolicy_name" to table: "rate_limit_policies"
CREATE UNIQUE INDEX "ratelimitpolicy_name" ON "rate_limit_policies" ("name") WHERE (tenant_id IS NULL);
-- Create index "ratelimitpolicy_tenant_id_name" to table: "rate_limit_policies"
CREATE UNIQUE INDEX "ratelimitpolicy_tenant_id_name" ON "rate_limit_policies" ("tenant_id", "name") WHERE (tenant_id IS NOT NULL);
-- Drop index "roles_name_key" from table: "roles"
DROP INDEX "roles_name_key";
-- Create index "role_name" to table: "roles"
CREATE UNIQUE INDEX "role_name" ON "roles" ("name") WHERE (tenant_id IS NULL);
-- Create index "role_tenant_id_name" to table: "roles"
CREATE UNIQUE INDEX "role_tenant_id_name" ON "roles" ("tenant_id", "name") WHERE (tenant_id IS NOT NULL);
