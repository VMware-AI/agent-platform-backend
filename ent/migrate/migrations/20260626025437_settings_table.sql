-- Create "settings" table
CREATE TABLE "settings" ("id" uuid NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "key" character varying NOT NULL, "value" character varying NULL, PRIMARY KEY ("id"));
-- Create index "settings_key_key" to table: "settings"
CREATE UNIQUE INDEX "settings_key_key" ON "settings" ("key");
