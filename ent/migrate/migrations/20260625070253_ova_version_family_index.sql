-- Rename the implicit "family" FK column to an explicit "family_id" (data- and
-- constraint-preserving — the FK follows the column rename) so it can be indexed.
ALTER TABLE "ova_template_versions" RENAME COLUMN "ova_template_family_versions" TO "family_id";
-- Create index "ovatemplateversion_family_id" to table: "ova_template_versions"
CREATE INDEX "ovatemplateversion_family_id" ON "ova_template_versions" ("family_id");
