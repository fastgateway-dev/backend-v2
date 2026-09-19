// Package leaderlock provides Postgres advisory-lock based leader election.
//
// The Phase 3a certificate distribution controller must run on exactly one
// replica while the rest of the API layer scales horizontally. Rather than
// depending on client-go/leaderelection (which requires a Kubernetes Lease
// object and is not otherwise used anywhere in this repo), leadership is
// decided with a single Postgres session-level advisory lock
// (pg_try_advisory_lock / pg_advisory_unlock): every replica repeatedly
// tries to acquire the same lock key on a dedicated *sql.Conn, and whichever
// replica holds that session-level lock is the leader.
package leaderlock

import (
	"context"
	"database/sql"
	"log"
	"sync/atomic"
	"time"
)

// CertDistributorLockKey is the fixed Postgres advisory lock key used to
// elect the single leader for the Phase 3a certificate distribution
// controller. It must stay constant across all replicas/deploys: every
// process that should compete for the same leadership role passes this same
// key to New. The value is arbitrary but deliberately encodes "fgc"
// (FastGateway Certificates) distribution role 01, so it reads as
// self-documenting rather than a random magic number, and is unlikely to
// collide with other advisory locks this application might use in future.
const CertDistributorLockKey int64 = 0x6667_63_01

// Gate is the thin interface the distribution controller depends on. It
// intentionally exposes nothing about Postgres or advisory locks so the
// controller can be unit tested against a trivial fake implementation
// instead of a real PostgresGate.
type Gate interface {
	IsLeader() bool
}

// PostgresGate implements Gate by holding a Postgres session-level advisory
// lock on a dedicated *sql.Conn pinned out of sqlDB's pool. Run must be
// started in its own goroutine; IsLeader is safe to call concurrently from
// any goroutine at any time (including before Run has ever run).
type PostgresGate struct {
	sqlDB *sql.DB
	key   int64
	retry time.Duration

	leader atomic.Bool

	// tryLock attempts to acquire the advisory lock and reports whether it
	// succeeded. unlock releases the lock (if held) and closes the pinned
	// connection. Both are unexported func fields rather than direct SQL
	// calls so that tests can drive PostgresGate.Run's loop logic (leader
	// flag transitions, retry behaviour, shutdown cleanup) without a real
	// Postgres instance. New wires them to the real SQL-backed
	// implementations; tests construct a PostgresGate directly and assign
	// their own fakes.
	tryLock func(ctx context.Context) (bool, error)
	unlock  func()
}

// New creates a PostgresGate that will compete for the advisory lock
// identified by key once Run is started, retrying on the given interval.
// sqlDB is the shared connection pool; a single connection is pinned out of
// it (via sqlDB.Conn) the first time Run attempts to acquire the lock, and
// is released back (closed) when Run's context is cancelled.
func New(sqlDB *sql.DB, key int64, retry time.Duration) *PostgresGate {
	g := &PostgresGate{
		sqlDB: sqlDB,
		key:   key,
		retry: retry,
	}

	var conn *sql.Conn

	g.tryLock = func(ctx context.Context) (bool, error) {
		if conn == nil {
			c, err := sqlDB.Conn(ctx)
			if err != nil {
				return false, err
			}
			conn = c
		}

		var acquired bool
		row := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key)
		if err := row.Scan(&acquired); err != nil {
			// The pinned connection is presumed dead (network blip, pool
			// eviction, Postgres restart, ...). Drop it so the next
			// attempt transparently pins a fresh one and tries again.
			_ = conn.Close()
			conn = nil
			return false, err
		}

		return acquired, nil
	}

	g.unlock = func() {
		if conn == nil {
			return
		}

		// Best effort: release the session-level lock explicitly. Even if
		// this fails (e.g. the connection is already broken), closing the
		// connection immediately below releases every advisory lock held
		// by the session regardless, so leadership is still relinquished.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			log.Printf("leaderlock: failed to release advisory lock %d: %v", key, err)
		}

		if err := conn.Close(); err != nil {
			log.Printf("leaderlock: failed to close pinned connection: %v", err)
		}
		conn = nil
	}

	return g
}

// IsLeader reports whether this process currently holds the advisory lock.
// Safe for concurrent use.
func (g *PostgresGate) IsLeader() bool {
	return g.leader.Load()
}

// Run drives the leader-election loop until ctx is cancelled. Callers
// should start it in its own goroutine, e.g. `go gate.Run(ctx)`.
//
// On each attempt: a successful tryLock sets the leader flag; a failed or
// erroring tryLock (including connection loss) clears it. The loop then
// waits for the retry interval (or ctx cancellation) before attempting
// again. Postgres advisory locks are re-entrant within the same session, so
// repeatedly calling pg_try_advisory_lock while already holding the lock is
// harmless and simply reconfirms leadership.
//
// When ctx is cancelled, Run clears the leader flag, releases the advisory
// lock and closes the pinned connection (via unlock), and returns -- no
// goroutine is leaked.
func (g *PostgresGate) Run(ctx context.Context) {
	ticker := time.NewTicker(g.retry)
	defer ticker.Stop()

	g.attempt(ctx)

	for {
		select {
		case <-ctx.Done():
			g.leader.Store(false)
			g.unlock()
			return
		case <-ticker.C:
			g.attempt(ctx)
		}
	}
}

// attempt makes a single try-lock call and updates the leader flag
// accordingly.
func (g *PostgresGate) attempt(ctx context.Context) {
	acquired, err := g.tryLock(ctx)
	if err != nil || !acquired {
		g.leader.Store(false)
		return
	}
	g.leader.Store(true)
}
