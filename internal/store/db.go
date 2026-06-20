// Package store opens the Ent client against postgres (prod) or an in-memory
// sqlite (dev/test). Auto-migration is opt-in: dev/sqlite migrate on open, prod
// uses reviewed versioned migrations (see ent/migrate) and never auto-alters.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver
	_ "modernc.org/sqlite"             // pure-Go sqlite driver (no CGO)

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// Open returns an Ent client. An empty databaseURL selects an in-memory sqlite
// database (dev/test); otherwise a postgres DSN is expected. autoMigrate runs
// ent's schema auto-migration — pass true for dev/sqlite, false for prod (apply
// versioned migrations out of band instead).
func Open(ctx context.Context, databaseURL string, autoMigrate bool) (*ent.Client, error) {
	if databaseURL == "" {
		db, err := sql.Open("sqlite", "file:agentplatform?mode=memory&cache=shared&_pragma=foreign_keys(1)")
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		return migrate(ctx, dialect.SQLite, db, autoMigrate)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return migrate(ctx, dialect.Postgres, db, autoMigrate)
}

func migrate(ctx context.Context, drv string, db *sql.DB, autoMigrate bool) (*ent.Client, error) {
	client := ent.NewClient(ent.Driver(entsql.OpenDB(drv, db)))
	if autoMigrate {
		if err := client.Schema.Create(ctx); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("schema migrate: %w", err)
		}
	}
	return client, nil
}
