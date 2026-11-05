# CPIM-MES v0.41.1 Step 6 — Login Rate Limit / Auth Audit

Baseline: Step 5 Cookie Auth / CORS / CSRF complete bundle

## Scope

Implemented Production Hardening Step 6 without changing business/planning semantics or database schema.

### Login throttling

- Protects both `/api/auth/login` and `/api/auth/token`.
- Independent failed-attempt buckets:
  - IP: 30 failures / 15 minutes by default.
  - normalized username: 5 failures / 15 minutes by default.
- Configuration is fail-closed for non-positive/invalid values.
- Threshold response: HTTP 429 + `Retry-After` seconds.
- Invalid credentials retain the generic `invalid username or password` response.
- Successful login clears only the username bucket; IP aggregate failures remain.
- Login JSON body capped at 8 KiB.
- Expired buckets are swept periodically; IP and username maps are each capped at 50,000 keys.
- In-process limiter is defense in depth; multi-replica deployments should also use a shared trusted-edge limiter.

### Authentication audit

Reuses migration 0004 `audit_log`; no migration added.

Dedicated events:

- `LOGIN_SUCCESS`
- `LOGIN_FAILURE`
- `LOGIN_THROTTLED`
- `LOGOUT`

Authentication bodies are excluded from generic request auditing. Dedicated payloads contain only safe metadata such as channel, limiter bucket, retry delay, and request ID. Password, JWT, Authorization header, session cookie and CSRF token are not recorded.

### Client IP boundary

The supported production topology keeps Backend internal and has Nginx overwrite `X-Real-IP`. Rate limit/audit IP identity uses valid `X-Real-IP` and ignores `X-Forwarded-For` selection. Directly exposing Backend while trusting forwarded headers remains unsupported.

## Configuration

```text
LOGIN_RATE_LIMIT_WINDOW=15m
LOGIN_RATE_LIMIT_IP_MAX_FAILURES=30
LOGIN_RATE_LIMIT_USERNAME_MAX_FAILURES=5
```

Updated:

- `backend/internal/config/config.go`
- `backend/internal/config/config_test.go`
- `backend/cmd/server/main.go`
- `docker-compose.yml`
- `docker-compose.prod.yml`
- `.env.example`
- `.env.production.example`

## HTTP / API changes

Updated:

- `backend/internal/api/auth.go`
- `backend/internal/api/router.go`
- `backend/internal/api/traceability.go`
- `backend/internal/api/openapi.json`

Added:

- `backend/internal/api/login_rate_limit.go`
- `backend/internal/api/login_rate_limit_test.go`

OpenAPI now documents 429 and `Retry-After` for browser and token login.

## Test / policy changes

Added:

- `backend/scripts/check_login_rate_limit_auth_audit.py`
- `e2e/tests/auth-rate-limit-audit.spec.ts`
- `docs/LOGIN_RATE_LIMIT_AUTH_AUDIT.md`

Updated CI and Makefile so Step 6 guard runs with the existing hardening/static gates.

The E2E suite now defines 21 tests total. The Step 6 test verifies generic 401, repeated failure -> 429, Retry-After, independent username/IP behavior, and persisted auth audit events without secrets.

## Validation completed in this environment

- Step 6 Static Guard: 57/57 PASS
- Production Config Guard: 41/41 PASS
- Demo Credentials Guard: 41/41 PASS
- Production Frontend Guard: 51/51 PASS
- Cookie Auth/CORS/CSRF Guard: 47/47 PASS
- 0039 Static Guard: 106 checks PASS
- 0040 Static Guard: 95/95 PASS
- 0041 Static Guard: 98/98 PASS
- All 30 `backend/scripts/check_*.py`: PASS
- Runtime version policy: PASS
- Dependency lock check: PASS
- CI/dependency policy: PASS
- OpenAPI JSON parse: PASS
- Compose/GitHub Actions YAML parse: PASS
- Isolated Config unit tests (stdlib-only compatibility harness): PASS
- Isolated Login Rate Limiter unit tests (stdlib-only compatibility harness): PASS

Step 5 baseline comparison:

```text
backend/internal/service      UNCHANGED
backend/internal/domain       UNCHANGED
backend/internal/repository   UNCHANGED
backend/migrations            UNCHANGED
frontend/src                  UNCHANGED
```

Migration state:

```text
0001-0041
count = 41
latest = 0041
0042 = absent
```

## Environment limitation

The execution environment has Go 1.23.2 and no network access to download the required Go 1.26 toolchain/modules, and no Docker daemon. Therefore these final runtime gates must execute on the development Mac or GitHub Actions:

```text
go mod verify
go vet ./...
go test -race ./...
frontend npm ci/typecheck/build
Docker production build
Playwright 21/21
```

The CI definition includes the Step 6 static gate and production-topology E2E path.
