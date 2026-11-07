package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/testdb"
	"github.com/jmoiron/sqlx"
)

func TestSessionAdvisoryLockSerialisesAndAlwaysReleases(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	const key = "test-lock:serialise"

	// Mutual exclusion across concurrent holders.
	var inside, maxInside atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := withSessionAdvisoryLock(ctx, db, key, func(conn *sqlx.Conn) (struct{}, error) {
				n := inside.Add(1)
				for {
					m := maxInside.Load()
					if n <= m || maxInside.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				inside.Add(-1)
				return struct{}{}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxInside.Load() != 1 {
		t.Fatalf("%d holders were inside the lock at once", maxInside.Load())
	}

	// An error from fn still releases the lock.
	boom := errors.New("boom")
	if _, err := withSessionAdvisoryLock(ctx, db, key, func(*sqlx.Conn) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error not returned: %v", err)
	}

	// A waiter whose context is cancelled gives up without leaking the lock.
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = withSessionAdvisoryLock(ctx, db, key, func(*sqlx.Conn) (int, error) {
			close(held)
			<-release
			return 0, nil
		})
	}()
	<-held
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := withSessionAdvisoryLock(cctx, db, key, func(*sqlx.Conn) (int, error) {
		t.Error("cancelled waiter must not run")
		return 0, nil
	}); err == nil {
		t.Fatal("cancelled waiter returned no error")
	}
	close(release)

	actx, acancel := context.WithTimeout(ctx, 5*time.Second)
	defer acancel()
	if _, err := withSessionAdvisoryLock(actx, db, key, func(*sqlx.Conn) (int, error) { return 0, nil }); err != nil {
		t.Fatalf("lock was leaked: %v", err)
	}
	var leftover int
	if err := db.GetContext(ctx, &leftover, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid::bigint=(hashtextextended($1,0) & x'ffffffff'::bigint)`, key); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("%d advisory locks still held after all holders finished", leftover)
	}
}
