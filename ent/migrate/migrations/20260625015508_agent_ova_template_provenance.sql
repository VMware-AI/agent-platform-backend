-- Modify "agents" table
ALTER TABLE "agents" ADD COLUMN "template_family_id" uuid NULL, ADD COLUMN "template_version_id" uuid NULL;
