-- Create index "requestlog_agent_id" to table: "request_logs"
CREATE INDEX "requestlog_agent_id" ON "request_logs" ("agent_id");
-- Create index "requestlog_user_id" to table: "request_logs"
CREATE INDEX "requestlog_user_id" ON "request_logs" ("user_id");
-- Create index "tokenusage_agent_id" to table: "token_usages"
CREATE INDEX "tokenusage_agent_id" ON "token_usages" ("agent_id");
-- Create index "tokenusage_department_id" to table: "token_usages"
CREATE INDEX "tokenusage_department_id" ON "token_usages" ("department_id");
-- Create index "tokenusage_tenant_id_created_at" to table: "token_usages"
CREATE INDEX "tokenusage_tenant_id_created_at" ON "token_usages" ("tenant_id", "created_at");
