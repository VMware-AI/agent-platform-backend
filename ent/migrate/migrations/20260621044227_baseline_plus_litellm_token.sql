-- Create "request_logs" table
CREATE TABLE "request_logs" ("id" uuid NOT NULL, "request_id" character varying NOT NULL, "user_id" uuid NULL, "agent_id" uuid NULL, "model" character varying NULL, "input_tokens" bigint NOT NULL DEFAULT 0, "output_tokens" bigint NOT NULL DEFAULT 0, "latency_ms" bigint NOT NULL DEFAULT 0, "status_code" bigint NOT NULL DEFAULT 200, "detail" character varying NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "requestlog_created_at" to table: "request_logs"
CREATE INDEX "requestlog_created_at" ON "request_logs" ("created_at");
-- Create index "requestlog_request_id" to table: "request_logs"
CREATE INDEX "requestlog_request_id" ON "request_logs" ("request_id");
-- Create index "requestlog_status_code" to table: "request_logs"
CREATE INDEX "requestlog_status_code" ON "request_logs" ("status_code");
-- Create "gateway_connections" table
CREATE TABLE "gateway_connections" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "endpoint" character varying NOT NULL, "master_key_ref" character varying NULL, "status" character varying NOT NULL DEFAULT 'disconnected', "load_balance_strategy" character varying NOT NULL DEFAULT 'simple_shuffle', PRIMARY KEY ("id"));
-- Create index "gateway_connections_name_key" to table: "gateway_connections"
CREATE UNIQUE INDEX "gateway_connections_name_key" ON "gateway_connections" ("name");
-- Create "agent_templates" table
CREATE TABLE "agent_templates" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "kind" character varying NOT NULL, "display" character varying NOT NULL, "description" character varying NULL, "install_method" character varying NOT NULL DEFAULT 'unset', "install_command" character varying NULL, "status" character varying NOT NULL DEFAULT 'deferred', "version" character varying NULL, PRIMARY KEY ("id"));
-- Create index "agent_templates_kind_key" to table: "agent_templates"
CREATE UNIQUE INDEX "agent_templates_kind_key" ON "agent_templates" ("kind");
-- Create "artifacts" table
CREATE TABLE "artifacts" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "kind" character varying NOT NULL, "version" character varying NOT NULL, "uri" character varying NOT NULL, "sha256" character varying NULL, "metadata" jsonb NULL, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "artifact_name_version" to table: "artifacts"
CREATE UNIQUE INDEX "artifact_name_version" ON "artifacts" ("name", "version");
-- Create "audit_logs" table
CREATE TABLE "audit_logs" ("id" uuid NOT NULL, "actor_user_id" uuid NULL, "action" character varying NOT NULL, "resource_type" character varying NULL, "resource_id" character varying NULL, "ip" character varying NULL, "result" character varying NOT NULL DEFAULT 'success', "detail" jsonb NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "auditlog_action" to table: "audit_logs"
CREATE INDEX "auditlog_action" ON "audit_logs" ("action");
-- Create index "auditlog_created_at" to table: "audit_logs"
CREATE INDEX "auditlog_created_at" ON "audit_logs" ("created_at");
-- Create "departments" table
CREATE TABLE "departments" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tenant_id" uuid NULL, "name" character varying NOT NULL, "litellm_team_id" character varying NULL, PRIMARY KEY ("id"));
-- Create "rate_limit_policies" table
CREATE TABLE "rate_limit_policies" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "rpm" bigint NULL, "tpm" bigint NULL, "enabled" boolean NOT NULL DEFAULT false, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "rate_limit_policies_name_key" to table: "rate_limit_policies"
CREATE UNIQUE INDEX "rate_limit_policies_name_key" ON "rate_limit_policies" ("name");
-- Create "images" table
CREATE TABLE "images" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "repository" character varying NOT NULL, "tag" character varying NOT NULL, "digest" character varying NULL, "signed" boolean NOT NULL DEFAULT false, PRIMARY KEY ("id"));
-- Create "memberships" table
CREATE TABLE "memberships" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" uuid NOT NULL, "department_id" uuid NOT NULL, "role" character varying NOT NULL DEFAULT 'user', PRIMARY KEY ("id"));
-- Create index "membership_department_id" to table: "memberships"
CREATE INDEX "membership_department_id" ON "memberships" ("department_id");
-- Create index "membership_user_id_department_id" to table: "memberships"
CREATE UNIQUE INDEX "membership_user_id_department_id" ON "memberships" ("user_id", "department_id");
-- Create "model_routes" table
CREATE TABLE "model_routes" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "model_alias" character varying NOT NULL, "gateway_connection_id" uuid NULL, "upstreams" jsonb NULL, "strategy" character varying NOT NULL DEFAULT 'simple_shuffle', "enabled" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "model_routes_name_key" to table: "model_routes"
CREATE UNIQUE INDEX "model_routes_name_key" ON "model_routes" ("name");
-- Create "agent_configs" table
CREATE TABLE "agent_configs" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "agent_type" character varying NOT NULL, "is_default" boolean NOT NULL DEFAULT false, "artifact_id" uuid NULL, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create "permissions" table
CREATE TABLE "permissions" ("id" uuid NOT NULL, "key" character varying NOT NULL, "description" character varying NULL, PRIMARY KEY ("id"));
-- Create index "permissions_key_key" to table: "permissions"
CREATE UNIQUE INDEX "permissions_key_key" ON "permissions" ("key");
-- Create "skills" table
CREATE TABLE "skills" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "version" character varying NOT NULL, "description" character varying NULL, "uri" character varying NOT NULL, PRIMARY KEY ("id"));
-- Create "resource_pools" table
CREATE TABLE "resource_pools" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "kind" character varying NOT NULL DEFAULT 'vcenter', "endpoint" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'disconnected', "secret_ref" character varying NULL, "datacenter_count" bigint NOT NULL DEFAULT 0, "cluster_count" bigint NOT NULL DEFAULT 0, "host_count" bigint NOT NULL DEFAULT 0, "vm_count" bigint NOT NULL DEFAULT 0, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create "virtual_keys" table
CREATE TABLE "virtual_keys" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "litellm_key" character varying NOT NULL, "litellm_token" character varying NULL, "alias" character varying NULL, "user_id" uuid NOT NULL, "agent_id" uuid NULL, "rate_limit_policy_id" uuid NULL, "team_id" character varying NULL, "models" jsonb NULL, "max_budget" double precision NULL, "status" character varying NOT NULL DEFAULT 'active', "expires_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "virtualkey_user_id" to table: "virtual_keys"
CREATE INDEX "virtualkey_user_id" ON "virtual_keys" ("user_id");
-- Create "router_tiers" table
CREATE TABLE "router_tiers" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tier" character varying NOT NULL, "model_alias" character varying NOT NULL, PRIMARY KEY ("id"));
-- Create index "routertier_tier" to table: "router_tiers"
CREATE UNIQUE INDEX "routertier_tier" ON "router_tiers" ("tier");
-- Create "agents" table
CREATE TABLE "agents" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "agent_type" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'provisioning', "owner_user_id" uuid NOT NULL, "vm_ref" character varying NULL, "config_id" uuid NULL, "virtual_key_id" uuid NULL, "resource_pool_id" uuid NULL, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "agent_owner_user_id" to table: "agents"
CREATE INDEX "agent_owner_user_id" ON "agents" ("owner_user_id");
-- Create index "agent_status" to table: "agents"
CREATE INDEX "agent_status" ON "agents" ("status");
-- Create "tenants" table
CREATE TABLE "tenants" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, PRIMARY KEY ("id"));
-- Create "token_usages" table
CREATE TABLE "token_usages" ("id" uuid NOT NULL, "user_id" uuid NOT NULL, "agent_id" uuid NULL, "model" character varying NOT NULL, "input_tokens" bigint NOT NULL DEFAULT 0, "output_tokens" bigint NOT NULL DEFAULT 0, "cost" double precision NULL DEFAULT 0, "correlation_id" character varying NULL, "tenant_id" uuid NULL, "department_id" uuid NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "tokenusage_created_at" to table: "token_usages"
CREATE INDEX "tokenusage_created_at" ON "token_usages" ("created_at");
-- Create index "tokenusage_model" to table: "token_usages"
CREATE INDEX "tokenusage_model" ON "token_usages" ("model");
-- Create index "tokenusage_user_id" to table: "token_usages"
CREATE INDEX "tokenusage_user_id" ON "token_usages" ("user_id");
-- Create "upstreams" table
CREATE TABLE "upstreams" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "provider" character varying NOT NULL, "api_base" character varying NULL, "api_key_ref" character varying NULL, "model" character varying NOT NULL, "enabled" boolean NOT NULL DEFAULT true, PRIMARY KEY ("id"));
-- Create index "upstreams_name_key" to table: "upstreams"
CREATE UNIQUE INDEX "upstreams_name_key" ON "upstreams" ("name");
-- Create "roles" table
CREATE TABLE "roles" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "name" character varying NOT NULL, "is_system" boolean NOT NULL DEFAULT false, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "roles_name_key" to table: "roles"
CREATE UNIQUE INDEX "roles_name_key" ON "roles" ("name");
-- Create "role_permissions" table
CREATE TABLE "role_permissions" ("role_id" uuid NOT NULL, "permission_id" uuid NOT NULL, PRIMARY KEY ("role_id", "permission_id"), CONSTRAINT "role_permissions_permission_id" FOREIGN KEY ("permission_id") REFERENCES "permissions" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "role_permissions_role_id" FOREIGN KEY ("role_id") REFERENCES "roles" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create "users" table
CREATE TABLE "users" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "username" character varying NOT NULL, "email" character varying NOT NULL, "password_hash" character varying NOT NULL, "role" character varying NOT NULL DEFAULT 'user', "tenant_id" uuid NULL, "must_change_password" boolean NOT NULL DEFAULT true, "is_active" boolean NOT NULL DEFAULT true, "last_login_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "user_tenant_id" to table: "users"
CREATE INDEX "user_tenant_id" ON "users" ("tenant_id");
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
-- Create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX "users_username_key" ON "users" ("username");
-- Create "user_roles" table
CREATE TABLE "user_roles" ("user_id" uuid NOT NULL, "role_id" uuid NOT NULL, PRIMARY KEY ("user_id", "role_id"), CONSTRAINT "user_roles_role_id" FOREIGN KEY ("role_id") REFERENCES "roles" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "user_roles_user_id" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
