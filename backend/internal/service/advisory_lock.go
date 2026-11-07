package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// txConn is what work running under withSessionAdvisoryLock uses to talk to
// PostgreSQL. Both *sqlx.DB and *sqlx.Conn satisfy it.
type txConn interface {
	BeginTxx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error)
	GetContext(ctx context.Context, dest any, query string, args ...any) error
}

const sessionAdvisoryUnlockTimeout = 5 * time.Second

// withSessionAdvisoryLock serialises callers that share key across all
// backend processes and runs fn while the lock is held.
//
// The lock is a session-level advisory lock taken on a dedicated connection
// BEFORE any transaction starts. A transaction-level lock taken inside a
// SERIALIZABLE/REPEATABLE READ transaction does not help: the transaction
// snapshot is fixed by the locking statement itself, so every waiter wakes up
// with a snapshot older than the previous holder's commit and fails with
// 40001. Here each holder begins its transaction only after acquiring the
// lock, so it sees everything earlier holders committed.
//
// fn must run all of its database work on conn so that a holder never needs a
// second pool connection while waiters occupy the pool. The connection is
// discarded rather than returned to the pool if the lock state is uncertain.
func withSessionAdvisoryLock[T any](ctx context.Context, db *sqlx.DB, key string, fn func(conn *sqlx.Conn) (T, error)) (T, error) {
	var zero T
	conn, err := db.Connx(ctx)
	if err != nil {
		return zero, err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		// A cancellation can race with a successful acquisition; never hand a
		// connection that might still hold the lock back to the pool.
		discardConn(conn)
		return zero, err
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionAdvisoryUnlockTimeout)
		defer cancel()
		var released bool
		if err := conn.GetContext(uctx, &released, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil || !released {
			discardConn(conn)
			return
		}
		_ = conn.Close()
	}()
	return fn(conn)
}

// discardConn closes the physical connection (ending its PostgreSQL session and
// with it any session-level lock) instead of returning it to the pool.
func discardConn(conn *sqlx.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func sessionLockKey(kind string, id fmt.Stringer) string {
	return kind + ":" + id.String()
}
