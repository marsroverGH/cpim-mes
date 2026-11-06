package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Serializable transactions can be aborted by PostgreSQL with SQLSTATE 40001
// (serialization_failure) or 40P01 (deadlock_detected) when they race with a
// concurrent transaction. Both are definitive rollbacks — nothing was
// committed — so re-running the whole transaction is safe and is the
// behaviour PostgreSQL documents for SERIALIZABLE.
const (
	txRetryMaxAttempts = 5
	txRetryBaseDelay   = 20 * time.Millisecond
	txRetryMaxDelay    = 500 * time.Millisecond
)

// txRetrySleep is replaced in tests to keep them fast.
var txRetrySleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// IsRetryableTxError reports whether err (anywhere in its chain) is a
// PostgreSQL serialization failure or deadlock.
func IsRetryableTxError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}

// retrySerializable runs fn, re-running it with jittered exponential backoff
// while it fails with a retryable transaction error. fn must own its whole
// transaction (BEGIN … COMMIT) so that each attempt starts from scratch.
// The last error is returned unchanged when attempts are exhausted.
func retrySerializable[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var (
		result T
		err    error
	)
	delay := txRetryBaseDelay
	for attempt := 1; ; attempt++ {
		result, err = fn()
		if err == nil || !IsRetryableTxError(err) || attempt >= txRetryMaxAttempts {
			return result, err
		}
		jittered := delay/2 + time.Duration(rand.Int64N(int64(delay)))
		if sleepErr := txRetrySleep(ctx, jittered); sleepErr != nil {
			return result, err
		}
		delay *= 2
		if delay > txRetryMaxDelay {
			delay = txRetryMaxDelay
		}
	}
}
