# Login Rate Limit / Authentication Audit

Target: **v0.41.1 Production Hardening — Step 6**

## Login throttling

Both authentication entry points are protected:

- `POST /api/auth/login` (browser cookie session)
- `POST /api/auth/token` (CLI/API Bearer token)

Failed credentials are counted in two independent in-process buckets:

| Bucket | Default | Window |
| --- | ---: | ---: |
| Client IP | 30 failures | 15 minutes |
| Normalized username | 5 failures | 15 minutes |

Configuration:

```text
LOGIN_RATE_LIMIT_WINDOW=15m
LOGIN_RATE_LIMIT_IP_MAX_FAILURES=30
LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES=5
```

All values must be positive. Invalid values fail configuration loading rather than silently disabling protection.

Before authentication the backend checks both buckets. Invalid credentials increment both. When either threshold is reached, the response is:

```text
HTTP 429 Too Many Requests
Retry-After: <seconds>
```

The 401 message remains generic (`invalid username or password`) and does not reveal whether a username exists.

A successful login clears only that normalized username's failure bucket. The IP bucket is deliberately retained so one valid credential cannot erase aggregate credential-stuffing failures from the same source address.

Unauthenticated login JSON bodies are capped at 8 KiB. Expired limiter buckets are swept periodically and each IP/username key map is hard-capped at 50,000 entries to bound process memory.

The limiter is local to each backend process. This preserves the v0.41.1 no-migration boundary. Multi-replica production should additionally enforce a shared rate limit at a trusted edge/API gateway; every backend replica still applies the local limiter as defense in depth.

## Client IP trust boundary

The supported production topology exposes only Nginx. Nginx overwrites `X-Real-IP` with its direct client address before proxying to the internal backend. Authentication rate limiting and audit use that value when it is a valid IP and ignore client-selected `X-Forwarded-For` chains.

Do not expose the production backend directly to untrusted clients while relying on forwarded headers.

## Authentication audit events

Step 6 reuses the existing `audit_log` table from migration `0004`; no new migration is created.

Dedicated events:

```text
LOGIN_SUCCESS
LOGIN_FAILURE
LOGIN_THROTTLED
LOGOUT
```

All use:

```text
resource = authentication
```

Recorded fields include username attempted/authenticated, role when known, HTTP status, client IP, timestamp, request ID, login channel, and for throttling only the bucket and retry delay.

Authentication request bodies are excluded from generic request auditing. Dedicated authentication audit payloads never include:

- password
- JWT
- Authorization header
- session cookie
- CSRF cookie/token

Audit writes are bounded by a short context timeout and do not change the authentication response if the audit store is temporarily unavailable.

## Validation

```bash
cd backend
go test -mod=readonly ./internal/config ./internal/api
cd ..
python3 backend/scripts/check_login_rate_limit_auth_audit.py
```

Playwright adds `e2e/tests/auth-rate-limit-audit.spec.ts`, which verifies generic 401 behavior, repeated failure -> 429, `Retry-After`, successful login after an unrelated username throttle, and persistence of the dedicated authentication audit events without secrets.

## Invariants

Step 6 does not add migration `0042`, modify migrations `0001`-`0041`, or change MRP, Detailed Scheduling, Pegging, Control Tower, or Recovery Planning semantics.
