# Versioned migrations

Reviewed SQL migrations applied to **production** postgres. Production never
auto-migrates — `DB_AUTO_MIGRATE` defaults off in prod (`internal/config`), so
the schema is changed only by applying the files in this directory. Dev/test on
in-memory sqlite still auto-migrate from the ent schema.

Requires the [Atlas CLI](https://atlasgo.io) + a throwaway dev postgres
(`ATLAS_DEV_URL`). See `atlas.hcl` and the Makefile `migrate-*` targets.

- Generate after a schema change: `ATLAS_DEV_URL=… make migrate-diff name=add_x`
- Apply to a target DB:           `DATABASE_URL=… make migrate-apply`
- Check drift/status:             `DATABASE_URL=… make migrate-status`

> The baseline migration (`name=init`) is generated against a real dev postgres
> and is **not committed yet** — that step needs the team's dev DB + Atlas CLI.
