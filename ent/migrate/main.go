//go:build ignore

// Versioned-migration generator (H3: prod never auto-migrates).
//
// Generates an Atlas-format migration file under ent/migrate/migrations by
// diffing the current Ent schema against the state replayed from the existing
// migration directory. Uses Ent's native Atlas integration (ariga.io/atlas,
// already a transitive dependency of Ent) rather than the atlas-provider-ent
// CLI plugin, which keeps generation self-contained and air-gap friendly.
//
// Usage (needs a throwaway dev postgres in ATLAS_DEV_URL):
//
//	ATLAS_DEV_URL=postgres://localhost:5432/atlas_dev?sslmode=disable \
//	  go run -mod=mod ./ent/migrate/main.go <migration_name>
//
// Or via the Makefile: `make migrate-diff name=<migration_name>`.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"

	atlas "ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver (registered as "pgx")

	"github.com/VMware-AI/agent-platform-backend/ent/migrate"
)

const migrationsDir = "ent/migrate/migrations"

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		log.Fatalln("migration name required: go run -mod=mod ./ent/migrate/main.go <name>")
	}
	name := os.Args[1]

	devURL := os.Getenv("ATLAS_DEV_URL")
	if devURL == "" {
		log.Fatalln("ATLAS_DEV_URL is required (throwaway dev postgres for diff computation)")
	}

	ctx := context.Background()

	dir, err := atlas.NewLocalDir(migrationsDir)
	if err != nil {
		log.Fatalf("open migration dir %q: %v", migrationsDir, err)
	}

	// Open the dev database with the same pgx driver the app uses, so the diff is
	// computed against a real postgres (Atlas replays the directory here).
	db, err := sql.Open("pgx", devURL)
	if err != nil {
		log.Fatalf("open dev db: %v", err)
	}
	defer func() { _ = db.Close() }()
	drv := entsql.OpenDB(dialect.Postgres, db)

	m, err := schema.NewMigrate(drv,
		schema.WithDir(dir),                         // emit Atlas-format files + atlas.sum
		schema.WithMigrationMode(schema.ModeReplay), // current state = replay of existing dir
		schema.WithDialect(dialect.Postgres),        // target dialect
		schema.WithFormatter(atlas.DefaultFormatter),
		schema.WithDropColumn(true), // capture column drops in future diffs
		schema.WithDropIndex(true),  // capture index/constraint drops in future diffs
		schema.WithErrNoPlan(true),  // surface "no changes" instead of silently no-op
	)
	if err != nil {
		log.Fatalf("new migrate: %v", err)
	}

	switch err := m.NamedDiff(ctx, name, migrate.Tables...); {
	case errors.Is(err, atlas.ErrNoPlan):
		log.Printf("no schema changes — migration directory is up to date")
	case err != nil:
		log.Fatalf("generate migration %q: %v", name, err)
	default:
		log.Printf("migration %q written to %s/", name, migrationsDir)
	}
}
