-- Drop the "settings" table now that PlatformSettings is retired and the
-- AGENT_USER env-injection has been moved to a startup env (cmd/server).
DROP TABLE IF EXISTS "settings";