-- Drop the partial unique index "gatewayconnection_is_default" from table:
-- "gateway_connections"
DROP INDEX IF EXISTS "gatewayconnection_is_default";
-- Modify "gateway_connections" table
ALTER TABLE "gateway_connections" DROP COLUMN "is_default";
ALTER TABLE "gateway_connections" DROP COLUMN "public_url";
-- Backfill any existing model_routes row whose gateway_connection_id was
-- never set (NULL). At least one gateway_connections row is required for the
-- NOT NULL constraint to apply; if none exists, the migration fails loudly
-- rather than silently NULLing the column. Operators must register a
-- gateway before running this on a populated database.
DO $$
DECLARE
  fallback_id uuid;
BEGIN
  SELECT "id" INTO fallback_id
  FROM "gateway_connections"
  ORDER BY "created_at" ASC
  LIMIT 1;
  IF fallback_id IS NULL THEN
    RAISE EXCEPTION 'cannot backfill model_routes.gateway_connection_id: no gateway_connections rows exist';
  END IF;
  UPDATE "model_routes"
     SET "gateway_connection_id" = fallback_id
   WHERE "gateway_connection_id" IS NULL;
END $$;
-- Modify "model_routes" table
ALTER TABLE "model_routes" ALTER COLUMN "gateway_connection_id" SET NOT NULL;