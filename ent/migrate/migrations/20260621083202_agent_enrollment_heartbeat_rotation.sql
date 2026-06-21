-- Create "agent_enrollments" table
CREATE TABLE "agent_enrollments" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "agent_id" uuid NOT NULL, "vm_id" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "enroll_token_hash" character varying NULL, "enroll_expires_at" timestamptz NOT NULL, "vm_token_hash" character varying NULL, "vm_token_issued_at" timestamptz NULL, "vm_token_expires_at" timestamptz NULL, "last_seen_at" timestamptz NULL, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "agent_enrollments_agent_id_key" to table: "agent_enrollments"
CREATE UNIQUE INDEX "agent_enrollments_agent_id_key" ON "agent_enrollments" ("agent_id");
-- Create index "agent_enrollments_vm_id_key" to table: "agent_enrollments"
CREATE UNIQUE INDEX "agent_enrollments_vm_id_key" ON "agent_enrollments" ("vm_id");
-- Create index "agentenrollment_status" to table: "agent_enrollments"
CREATE INDEX "agentenrollment_status" ON "agent_enrollments" ("status");
-- Create "agent_heartbeats" table
CREATE TABLE "agent_heartbeats" ("id" uuid NOT NULL, "agent_id" uuid NOT NULL, "reported_at" timestamptz NOT NULL, "received_at" timestamptz NOT NULL, "status" character varying NOT NULL, "agent_version" character varying NULL, "rotation_state" character varying NULL, "detail" jsonb NULL, PRIMARY KEY ("id"));
-- Create index "agentheartbeat_agent_id" to table: "agent_heartbeats"
CREATE INDEX "agentheartbeat_agent_id" ON "agent_heartbeats" ("agent_id");
-- Create index "agentheartbeat_received_at" to table: "agent_heartbeats"
CREATE INDEX "agentheartbeat_received_at" ON "agent_heartbeats" ("received_at");
-- Create "rotation_commands" table
CREATE TABLE "rotation_commands" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "command_id" character varying NOT NULL, "agent_id" uuid NOT NULL, "kind" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "reason" character varying NULL, "dispatched_at" timestamptz NULL, "acked_at" timestamptz NULL, "completed_at" timestamptz NULL, "result_fingerprint" character varying NULL, "secret_ref" character varying NULL, "error" character varying NULL, "tenant_id" uuid NULL, PRIMARY KEY ("id"));
-- Create index "rotation_commands_command_id_key" to table: "rotation_commands"
CREATE UNIQUE INDEX "rotation_commands_command_id_key" ON "rotation_commands" ("command_id");
-- Create index "rotationcommand_agent_id" to table: "rotation_commands"
CREATE INDEX "rotationcommand_agent_id" ON "rotation_commands" ("agent_id");
-- Create index "rotationcommand_agent_id_status" to table: "rotation_commands"
CREATE INDEX "rotationcommand_agent_id_status" ON "rotation_commands" ("agent_id", "status");
-- Create index "rotationcommand_status" to table: "rotation_commands"
CREATE INDEX "rotationcommand_status" ON "rotation_commands" ("status");
