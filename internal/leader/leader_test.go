package leader

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // pure-Go sqlite driver (no CGO)
)

// Without a Postgres backend the pg_try_advisory_lock function does not exist, so
// the lock query errors. IsLeader must then fail SAFE — return false rather than
// assume leadership — so two replicas never both run the guarded job. This pins
// the error-path contract without needing a live Postgres.
func TestPGLease_IsLeader_FailsSafeWithoutPostgres(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	l := NewPGLease(db, 42)
	if l.IsLeader(context.Background()) {
		t.Fatal("IsLeader must return false when the advisory lock cannot be acquired")
	}
	// Release on a never-acquired lease is a safe no-op (must not panic).
	l.Release(context.Background())
}
