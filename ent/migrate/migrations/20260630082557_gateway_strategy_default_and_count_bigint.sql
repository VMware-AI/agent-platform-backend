-- Modify "gateway_connections" table
ALTER TABLE "gateway_connections" ALTER COLUMN "load_balance_strategy" SET DEFAULT 'SIMPLE_SHUFFLE', ALTER COLUMN "backend_model_count" TYPE bigint;
-- Modify "model_routes" table
ALTER TABLE "model_routes" ALTER COLUMN "strategy" SET DEFAULT 'SIMPLE_SHUFFLE';
