-- Modify "model_routes" table
ALTER TABLE "model_routes" ADD COLUMN "gateway_name" character varying NULL DEFAULT '', ADD COLUMN "ui_strategy" character varying NOT NULL DEFAULT 'ROUND_ROBIN';
-- Modify "resource_pools" table
ALTER TABLE "resource_pools" ADD COLUMN "content_library_name" character varying NULL DEFAULT '';
