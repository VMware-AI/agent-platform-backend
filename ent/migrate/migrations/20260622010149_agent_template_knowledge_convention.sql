-- Modify "agent_templates" table
ALTER TABLE "agent_templates" ADD COLUMN "knowledge_root" character varying NULL, ADD COLUMN "knowledge_prompt" character varying NULL;
