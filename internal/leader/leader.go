// Package leader elects a single active replica for background jobs via a
// Postgres session-level advisory lock. Use it to single-flight work that must
// run on exactly one replica at a time — e.g. the gateway-key reconciler, whose
// destructive prune would otherwise race across replicas.
package leader

import (
	"context"
	"database/sql"
	"log"
)

// PGLease is a process-lifetime leader lease backed by a Postgres session-level
// advisory lock (pg_advisory_lock). At most one session across all replicas can
// hold a given lock key, so at most one replica is leader. The lock is held on a
// dedicated *sql.Conn for the process lifetime; if the process dies the
// connection drops and Postgres releases the lock automatically, letting another
// replica take over (failover). Not safe for concurrent use — call from a single
// goroutine (the background job's loop).
type PGLease struct {
	db   *sql.DB
	key  int64
	conn *sql.Conn // non-nil only while this replica holds the lock
}

// NewPGLease builds a lease for the given advisory-lock key. The key must be the
// same across all replicas of the job and distinct from other jobs' keys.
func NewPGLease(db *sql.DB, key int64) *PGLease {
	return &PGLease{db: db, key: key}
}

// IsLeader reports whether this replica currently holds the lease, (re)acquiring
// it when free. Safe to call every cycle: once held it cheaply verifies the
// holding connection is still alive and returns true without re-locking; if that
// connection died it drops it and re-attempts acquisition (failover). On ANY
// error it returns false — fail-safe, so the guarded job is skipped rather than
// risking two concurrent leaders.
func (l *PGLease) IsLeader(ctx context.Context) bool {
	// Already leader? Confirm the holding connection is still healthy; the
	// advisory lock lives only as long as this exact session.
	if l.conn != nil {
		if err := l.conn.PingContext(ctx); err == nil {
			return true
		}
		// Connection lost → Postgres already released the lock server-side. Drop
		// the dead conn and fall through to re-acquire.
		_ = l.conn.Close()
		l.conn = nil
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		log.Printf("leader: acquire connection failed: %v", err)
		return false
	}
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&got); err != nil {
		log.Printf("leader: pg_try_advisory_lock failed: %v", err)
		_ = conn.Close()
		return false
	}
	if !got {
		// Another replica holds it — return the connection to the pool.
		_ = conn.Close()
		return false
	}
	l.conn = conn // hold the connection (and thus the lock) until Release / death
	return true
}

// Release unlocks and returns the dedicated connection to the pool. Idempotent —
// a no-op when this replica is not currently the leader.
func (l *PGLease) Release(ctx context.Context) {
	if l.conn == nil {
		return
	}
	if _, err := l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.key); err != nil {
		log.Printf("leader: pg_advisory_unlock failed: %v", err)
	}
	_ = l.conn.Close()
	l.conn = nil
}
