package api

import (
	"strings"
	"sync"
	"time"
)

type loginRateLimitConfig struct {
	Window              time.Duration
	IPMaxFailures       int
	UsernameMaxFailures int
}

type loginFailureBucket struct {
	Failures int
	ResetAt  time.Time
}

type loginRateDecision struct {
	Blocked    bool
	Bucket     string
	RetryAfter time.Duration
}

// loginRateLimiter tracks failed authentication attempts in two independent
// buckets: client IP and normalized username. It intentionally lives in the
// application process so it can protect authentication before any successful
// identity exists and without adding a schema migration in v0.41.1.
//
// Production deployments with multiple backend replicas should place a shared
// edge limiter in front as defense in depth; each backend replica still applies
// this local limiter independently.
const maxTrackedLoginKeys = 50000

type loginRateLimiter struct {
	mu         sync.Mutex
	cfg        loginRateLimitConfig
	ip         map[string]loginFailureBucket
	username   map[string]loginFailureBucket
	now        func() time.Time
	operations uint64
}

func newLoginRateLimiter(cfg loginRateLimitConfig) *loginRateLimiter {
	if cfg.Window <= 0 {
		cfg.Window = 15 * time.Minute
	}
	if cfg.IPMaxFailures <= 0 {
		cfg.IPMaxFailures = 30
	}
	if cfg.UsernameMaxFailures <= 0 {
		cfg.UsernameMaxFailures = 5
	}
	return &loginRateLimiter{
		cfg:      cfg,
		ip:       make(map[string]loginFailureBucket),
		username: make(map[string]loginFailureBucket),
		now:      time.Now,
	}
}

func (l *loginRateLimiter) Check(ip, username string) loginRateDecision {
	if l == nil {
		return loginRateDecision{}
	}
	now := l.now()
	ipKey := strings.TrimSpace(ip)
	userKey := normalizeLoginUsername(username)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.maintenance(now)

	if decision := blockedDecision(l.ip, ipKey, l.cfg.IPMaxFailures, now, "ip"); decision.Blocked {
		return decision
	}
	return blockedDecision(l.username, userKey, l.cfg.UsernameMaxFailures, now, "username")
}

func (l *loginRateLimiter) RecordFailure(ip, username string) loginRateDecision {
	if l == nil {
		return loginRateDecision{}
	}
	now := l.now()
	ipKey := strings.TrimSpace(ip)
	userKey := normalizeLoginUsername(username)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.maintenance(now)

	ensureBucketCapacity(l.ip, ipKey, now)
	ensureBucketCapacity(l.username, userKey, now)
	ipBucket := incrementFailureBucket(l.ip, ipKey, now, l.cfg.Window)
	userBucket := incrementFailureBucket(l.username, userKey, now, l.cfg.Window)

	if ipKey != "" && ipBucket.Failures >= l.cfg.IPMaxFailures {
		return loginRateDecision{Blocked: true, Bucket: "ip", RetryAfter: positiveRemaining(ipBucket.ResetAt, now)}
	}
	if userKey != "" && userBucket.Failures >= l.cfg.UsernameMaxFailures {
		return loginRateDecision{Blocked: true, Bucket: "username", RetryAfter: positiveRemaining(userBucket.ResetAt, now)}
	}
	return loginRateDecision{}
}

// RecordSuccess clears only the username bucket. The IP bucket deliberately
// retains aggregate failures so one valid credential cannot reset protection
// against credential stuffing from the same source address.
func (l *loginRateLimiter) RecordSuccess(username string) {
	if l == nil {
		return
	}
	key := normalizeLoginUsername(username)
	if key == "" {
		return
	}
	l.mu.Lock()
	delete(l.username, key)
	l.mu.Unlock()
}

func (l *loginRateLimiter) maintenance(now time.Time) {
	l.operations++
	if l.operations%64 != 0 {
		return
	}
	deleteExpiredBuckets(l.ip, now)
	deleteExpiredBuckets(l.username, now)
}

func deleteExpiredBuckets(store map[string]loginFailureBucket, now time.Time) {
	for key, bucket := range store {
		if !now.Before(bucket.ResetAt) {
			delete(store, key)
		}
	}
}

func ensureBucketCapacity(store map[string]loginFailureBucket, key string, _ time.Time) {
	if key == "" {
		return
	}
	if _, exists := store[key]; exists || len(store) < maxTrackedLoginKeys {
		return
	}
	// Hard-cap unauthenticated memory. Expired entries are swept periodically;
	// if the cap is still full, evict one tracked key in O(1)-amortized time.
	// Large distributed attacks should additionally be limited at the edge.
	for victim := range store {
		delete(store, victim)
		break
	}
}

func normalizeLoginUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func blockedDecision(store map[string]loginFailureBucket, key string, max int, now time.Time, bucketName string) loginRateDecision {
	if key == "" {
		return loginRateDecision{}
	}
	b, ok := store[key]
	if !ok {
		return loginRateDecision{}
	}
	if !now.Before(b.ResetAt) {
		delete(store, key)
		return loginRateDecision{}
	}
	if b.Failures < max {
		return loginRateDecision{}
	}
	return loginRateDecision{Blocked: true, Bucket: bucketName, RetryAfter: positiveRemaining(b.ResetAt, now)}
}

func incrementFailureBucket(store map[string]loginFailureBucket, key string, now time.Time, window time.Duration) loginFailureBucket {
	if key == "" {
		return loginFailureBucket{}
	}
	b, ok := store[key]
	if !ok || !now.Before(b.ResetAt) {
		b = loginFailureBucket{ResetAt: now.Add(window)}
	}
	b.Failures++
	store[key] = b
	return b
}

func positiveRemaining(resetAt, now time.Time) time.Duration {
	remaining := resetAt.Sub(now)
	if remaining <= 0 {
		return time.Second
	}
	return remaining
}
