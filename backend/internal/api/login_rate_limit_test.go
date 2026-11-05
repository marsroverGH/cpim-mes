package api

import (
	"testing"
	"time"
)

func TestLoginRateLimiterUsesIndependentUsernameAndIPBuckets(t *testing.T) {
	start := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	limiter := newLoginRateLimiter(loginRateLimitConfig{
		Window:              10 * time.Minute,
		IPMaxFailures:       4,
		UsernameMaxFailures: 2,
	})
	limiter.now = func() time.Time { return start }

	if d := limiter.RecordFailure("192.0.2.10", "Alice"); d.Blocked {
		t.Fatalf("first failure unexpectedly blocked: %#v", d)
	}
	if d := limiter.RecordFailure("192.0.2.11", " alice "); !d.Blocked || d.Bucket != "username" {
		t.Fatalf("username bucket decision = %#v", d)
	}

	// A different username is not blocked by Alice's username bucket.
	if d := limiter.Check("192.0.2.12", "bob"); d.Blocked {
		t.Fatalf("unrelated username unexpectedly blocked: %#v", d)
	}

	// Aggregate failures across usernames still trip the IP bucket.
	if d := limiter.RecordFailure("192.0.2.50", "u1"); d.Blocked {
		t.Fatalf("ip failure 1 unexpectedly blocked: %#v", d)
	}
	if d := limiter.RecordFailure("192.0.2.50", "u2"); d.Blocked {
		t.Fatalf("ip failure 2 unexpectedly blocked: %#v", d)
	}
	if d := limiter.RecordFailure("192.0.2.50", "u3"); d.Blocked {
		t.Fatalf("ip failure 3 unexpectedly blocked: %#v", d)
	}
	if d := limiter.RecordFailure("192.0.2.50", "u4"); !d.Blocked || d.Bucket != "ip" {
		t.Fatalf("ip bucket decision = %#v", d)
	}
}

func TestLoginRateLimiterSuccessResetsUsernameButNotIP(t *testing.T) {
	start := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	limiter := newLoginRateLimiter(loginRateLimitConfig{
		Window:              10 * time.Minute,
		IPMaxFailures:       3,
		UsernameMaxFailures: 2,
	})
	limiter.now = func() time.Time { return start }

	_ = limiter.RecordFailure("192.0.2.20", "alice")
	limiter.RecordSuccess("ALICE")
	if d := limiter.Check("192.0.2.99", "alice"); d.Blocked {
		t.Fatalf("username bucket was not reset: %#v", d)
	}

	_ = limiter.RecordFailure("192.0.2.20", "bob")
	if d := limiter.RecordFailure("192.0.2.20", "carol"); !d.Blocked || d.Bucket != "ip" {
		t.Fatalf("ip failures should survive successful username reset: %#v", d)
	}
}

func TestLoginRateLimiterWindowExpires(t *testing.T) {
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	limiter := newLoginRateLimiter(loginRateLimitConfig{
		Window:              time.Minute,
		IPMaxFailures:       10,
		UsernameMaxFailures: 2,
	})
	limiter.now = func() time.Time { return now }

	_ = limiter.RecordFailure("192.0.2.30", "alice")
	if d := limiter.RecordFailure("192.0.2.31", "alice"); !d.Blocked {
		t.Fatalf("username should be blocked: %#v", d)
	}
	now = now.Add(time.Minute + time.Second)
	if d := limiter.Check("192.0.2.31", "alice"); d.Blocked {
		t.Fatalf("expired bucket remained blocked: %#v", d)
	}
}
