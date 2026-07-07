-- Drop the partial unique index "routertier_tier" from table:
-- "router_tiers"
DROP INDEX IF EXISTS "routertier_tier";
-- Modify "router_tiers" table
DROP TABLE IF EXISTS "router_tiers";