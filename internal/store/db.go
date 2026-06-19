// Package store opens the Ent client against postgres (prod) or an in-memory
// sqlite (dev/test), running the schema migration on open.
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

// Open returns a migrated Ent client. An empty databaseURL selects an in-memory
// sqlite database (dev/test); otherwise a postgres DSN is expected.
func Open(ctx context.Context, databaseURL string) (*ent.Client, error) {
	if databaseURL == "" {
		db, err := sql.Open("sqlite", "file:agentplatform?mode=memory&cache=shared&_pragma=foreign_keys(1)")
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		return migrate(ctx, dialect.SQLite, db)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return migrate(ctx, dialect.Postgres, db)
}

func migrate(ctx context.Context, drv string, db *sql.DB) (*ent.Client, error) {
	client := ent.NewClient(ent.Driver(entsql.OpenDB(drv, db)))
	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("schema migrate: %w", err)
	}
	return client, nil
}
