// Package store opens the Ent client against postgres (prod) or an in-memory
// sqlite (dev/test). Auto-migration is opt-in: dev/sqlite migrate on open, prod
// uses reviewed versioned migrations (see ent/migrate) and never auto-alters.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver
	_ "modernc.org/sqlite"             // pure-Go sqlite driver (no CGO)

	"github.com/VMware-AI/agent-platform-backend/ent"
)

// PoolConfig bounds the postgres connection pool. It is applied only on the
// postgres path; the dev/test sqlite path ignores it (a single shared in-memory
// connection). A zero MaxOpenConns/MaxIdleConns keeps Go's default behaviour
// (unlimited / 2); a zero ConnMaxLifetime never recycles.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DefaultPoolConfig gives finite bounds so an untuned deployment cannot open
// connections without ceiling (Go's MaxOpenConns default is unlimited).
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{MaxOpenConns: 20, MaxIdleConns: 10, ConnMaxLifetime: 30 * time.Minute}
}

// Open returns an Ent client with default pool bounds. An empty databaseURL
// selects an in-memory sqlite database (dev/test); otherwise a postgres DSN is
// expected. autoMigrate runs ent's schema auto-migration — pass true for
// dev/sqlite, false for prod (apply versioned migrations out of band instead).
func Open(ctx context.Context, databaseURL string, autoMigrate bool) (*ent.Client, error) {
	client, _, err := OpenWithPool(ctx, databaseURL, autoMigrate, DefaultPoolConfig())
	return client, err
}

// OpenWithPool is Open with explicit pool bounds. It also returns the underlying
// *sql.DB so the caller can run pool-level operations the Ent client does not
// expose — notably a Postgres advisory-lock leader lease for background jobs.
// The caller closes the Ent client (which closes the *sql.DB); do not close the
// returned *sql.DB separately.
func OpenWithPool(ctx context.Context, databaseURL string, autoMigrate bool, pool PoolConfig) (*ent.Client, *sql.DB, error) {
	if databaseURL == "" {
		db, err := sql.Open("sqlite", "file:agentplatform?mode=memory&cache=shared&_pragma=foreign_keys(1)")
		if err != nil {
			return nil, nil, fmt.Errorf("open sqlite: %w", err)
		}
		client, err := migrate(ctx, dialect.SQLite, db, autoMigrate)
		return client, db, err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}
	// Bound the pool so concurrent load (and multiple replicas) cannot exhaust
	// Postgres max_connections, and recycle connections for failover / PgBouncer.
	db.SetMaxOpenConns(pool.MaxOpenConns)
	db.SetMaxIdleConns(pool.MaxIdleConns)
	db.SetConnMaxLifetime(pool.ConnMaxLifetime)
	client, err := migrate(ctx, dialect.Postgres, db, autoMigrate)
	return client, db, err
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
