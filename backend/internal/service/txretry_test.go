package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func withInstantRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	old := txRetrySleep
	txRetrySleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	t.Cleanup(func() { txRetrySleep = old })
	return &slept
}

func serializationFailure() error {
	return fmt.Errorf("commit: %w", &pgconn.PgError{Code: "40001", Message: "could not serialize access"})
}

func TestIsRetryableTxError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"serialization": {serializationFailure(), true},
		"deadlock":      {&pgconn.PgError{Code: "40P01"}, true},
		"unique":        {&pgconn.PgError{Code: "23505"}, false},
		"business":      {&pgconn.PgError{Code: "P0001"}, false},
		"plain":         {errors.New("boom"), false},
		"nil":           {nil, false},
	}
	for name, tc := range cases {
		if got := IsRetryableTxError(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func TestRetrySerializableRetriesUntilSuccess(t *testing.T) {
	slept := withInstantRetrySleep(t)
	calls := 0
	got, err := retrySerializable(context.Background(), func() (string, error) {
		calls++
		if calls < 3 {
			return "", serializationFailure()
		}
		return "ok", nil
	})
	if err != nil || got != "ok" || calls != 3 || len(*slept) != 2 {
		t.Fatalf("got %q err=%v calls=%d slept=%v", got, err, calls, *slept)
	}
	for _, d := range *slept {
		if d <= 0 || d > txRetryMaxDelay*3/2 {
			t.Fatalf("unexpected backoff %v", d)
		}
	}
}

func TestRetrySerializableGivesUpAfterMaxAttempts(t *testing.T) {
	withInstantRetrySleep(t)
	calls := 0
	_, err := retrySerializable(context.Background(), func() (int, error) {
		calls++
		return 0, serializationFailure()
	})
	if calls != txRetryMaxAttempts || !IsRetryableTxError(err) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetrySerializableDoesNotRetryOtherErrors(t *testing.T) {
	withInstantRetrySleep(t)
	calls := 0
	want := &pgconn.PgError{Code: "P0001", Message: "business rule"}
	_, err := retrySerializable(context.Background(), func() (int, error) {
		calls++
		return 0, want
	})
	if calls != 1 || !errors.Is(err, want) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetrySerializableStopsWhenContextIsCancelled(t *testing.T) {
	withInstantRetrySleep(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := retrySerializable(ctx, func() (int, error) {
		calls++
		return 0, serializationFailure()
	})
	if calls != 1 || !IsRetryableTxError(err) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
