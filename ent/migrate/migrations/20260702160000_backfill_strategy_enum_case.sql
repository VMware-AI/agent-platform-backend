-- Backfill for 20260630150503 (#97): the strategy enums were unified to
-- UPPER_SNAKE (afca0ec) but that migration only changed the column DEFAULTs.
-- Rows written before it still hold the legacy lowercase spellings, which the
-- GraphQL enum can no longer serialize — clients get an invalid enum value.
-- Ent stores enums as varchar, so this is a plain value rewrite; unknown
-- values are left untouched on purpose (fail loudly rather than guess).
UPDATE "model_routes" SET "strategy" = CASE "strategy"
  WHEN 'simple_shuffle' THEN 'SIMPLE_SHUFFLE'
  WHEN 'latency'        THEN 'LATENCY_BASED_ROUTING'
  WHEN 'usage_v2'       THEN 'USAGE_BASED_ROUTING_V2'
  WHEN 'least_busy'     THEN 'LEAST_BUSY'
  WHEN 'cost'           THEN 'COST_BASED_ROUTING'
  ELSE "strategy" END
WHERE "strategy" IN ('simple_shuffle', 'latency', 'usage_v2', 'least_busy', 'cost');
-- Same unification, same legacy values (gateway_connections.load_balance_strategy).
UPDATE "gateway_connections" SET "load_balance_strategy" = CASE "load_balance_strategy"
  WHEN 'simple_shuffle' THEN 'SIMPLE_SHUFFLE'
  WHEN 'latency'        THEN 'LATENCY_BASED_ROUTING'
  WHEN 'usage_v2'       THEN 'USAGE_BASED_ROUTING_V2'
  WHEN 'least_busy'     THEN 'LEAST_BUSY'
  WHEN 'cost'           THEN 'COST_BASED_ROUTING'
  ELSE "load_balance_strategy" END
WHERE "load_balance_strategy" IN ('simple_shuffle', 'latency', 'usage_v2', 'least_busy', 'cost');
