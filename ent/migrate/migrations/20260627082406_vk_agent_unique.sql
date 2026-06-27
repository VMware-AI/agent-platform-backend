-- Create index "virtualkey_agent_id" to table: "virtual_keys"
CREATE UNIQUE INDEX "virtualkey_agent_id" ON "virtual_keys" ("agent_id") WHERE ((status)::text <> 'revoked'::text);
