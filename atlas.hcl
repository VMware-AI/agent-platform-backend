# Atlas config for ent versioned migrations (H3: prod never auto-migrates).
#   Generate:  ATLAS_DEV_URL=postgres://… make migrate-diff name=<change>
#   Apply:     DATABASE_URL=postgres://…  make migrate-apply
#   Status:    DATABASE_URL=postgres://…  make migrate-status
# Docs: https://atlasgo.io/guides/orms/ent

data "external_schema" "ent" {
  program = [
    "go", "run", "-mod=mod",
    "ariga.io/atlas-provider-ent",
    "--path", "./ent/schema",
    "--dialect", "postgres",
  ]
}

env "ent" {
  src = data.external_schema.ent.url
  # Throwaway dev DB Atlas uses to compute the diff. Point at a native postgres
  # scratch DB (Docker Desktop is flaky locally); e.g.
  #   ATLAS_DEV_URL=postgres://localhost:5432/atlas_dev?sslmode=disable
  dev = getenv("ATLAS_DEV_URL")
  migration {
    dir = "file://ent/migrate/migrations"
  }
  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
