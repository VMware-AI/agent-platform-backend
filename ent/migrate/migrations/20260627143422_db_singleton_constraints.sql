-- Pre-flight de-dup so the partial unique index can be created even if the old
-- racy insert-then-clear path left more than one default gateway: keep the most
-- recently created default, clear the rest. No-op on a clean DB.
UPDATE "gateway_connections" SET "is_default" = false
WHERE "is_default" AND "id" NOT IN (
  SELECT "id" FROM "gateway_connections" WHERE "is_default"
  ORDER BY "created_at" DESC LIMIT 1
);
-- Create index "gatewayconnection_is_default" to table: "gateway_connections"
CREATE UNIQUE INDEX "gatewayconnection_is_default" ON "gateway_connections" ("is_default") WHERE is_default;
-- Pre-flight de-dup: terminate older in-flight rotations per (agent_id, kind),
-- keeping the most recent, so the partial unique index can be created even if the
-- old EXISTS-then-INSERT race enqueued duplicates. No-op on a clean DB.
UPDATE "rotation_commands" SET "status" = 'failed', "error" = 'superseded by db_singleton_constraints migration de-dup'
WHERE "status" NOT IN ('completed', 'failed') AND "id" NOT IN (
  SELECT DISTINCT ON ("agent_id", "kind") "id" FROM "rotation_commands"
  WHERE "status" NOT IN ('completed', 'failed')
  ORDER BY "agent_id", "kind", "created_at" DESC
);
-- Create index "rotationcommand_agent_id_kind" to table: "rotation_commands"
CREATE UNIQUE INDEX "rotationcommand_agent_id_kind" ON "rotation_commands" ("agent_id", "kind") WHERE (((status)::text <> 'completed'::text) AND ((status)::text <> 'failed'::text));
