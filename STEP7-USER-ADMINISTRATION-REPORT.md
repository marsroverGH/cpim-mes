# CPIM-MES v0.41.1 Step 7 — User Administration

Baseline: Step 6 Login Rate Limit / Auth Audit complete bundle

## Scope

Implemented Production Hardening Step 7 without changing planning/business semantics or database schema.

### User administration API

Added Admin-only capability `admin.users.manage` and endpoints:

- `GET /api/admin/users`
- `POST /api/admin/users`
- `PUT /api/admin/users/{id}/role`
- `PUT /api/admin/users/{id}/active`
- `POST /api/admin/users/{id}/reset-password`
- `POST /api/auth/change-password` (authenticated self-service)

Only the `admin` role receives `admin.users.manage`. The frontend hides Admin navigation for non-admin users, but Backend RBAC is authoritative.

### Password policy

Managed user passwords use the existing bcrypt authentication model and are validated as:

- minimum: 15 UTF-8 bytes
- maximum: 72 UTF-8 bytes
- no NUL, CR, or LF characters

Usernames are trimmed, required, limited to 128 UTF-8 bytes, and reject control characters. Roles are restricted to `admin`, `planner`, `operator`, or `viewer`.

### Last-active-admin invariant

Disabling or demoting the final active administrator returns HTTP 409.

The protection is race-safe at the application SQL boundary:

1. start transaction
2. acquire shared User Administration PostgreSQL advisory transaction lock
3. lock target user row with `FOR UPDATE`
4. if removing an active admin, count another active admin
5. reject if none exists
6. update and commit

Role and active-status mutations share the same advisory lock, preventing concurrent disable/demote requests from both passing a stale count.

No Migration 0042 is added. Existing `users` schema is reused.

### Immediate authorization effect

The existing auth middleware verifies the JWT and reloads the user by ID from the database on every authenticated request. Therefore:

- role changes affect authorization on the next request
- disabling a user invalidates both cookie and Bearer access on the next request

No wait for the JWT's original expiration is required for role/active changes.

### Password operations

Admin reset stores only a newly generated bcrypt hash. Self-service password change locks the user row, verifies the current password, and updates the hash in the same transaction.

A password reset/change does not globally revoke already-issued Bearer JWTs because v0.41.1 does not add persistent session-version state. Browser self-service UI logs out immediately after password change. For immediate invalidation of an existing user session, administrators can disable the user; active status is checked on every request.

### Audit

Dedicated events reuse existing migration 0004 `audit_log`:

- `USER_CREATED`
- `ROLE_CHANGED`
- `USER_DISABLED`
- `USER_ENABLED`
- `PASSWORD_RESET`
- `PASSWORD_CHANGED`

`/api/admin/users*` request bodies are excluded from generic mutation-body audit, and `/api/auth/*` bodies were already excluded. Dedicated audit payloads contain target ID/username, target role/active state where relevant, actor, request ID, HTTP status and IP address; they do not contain passwords, password hashes, JWTs, cookies, CSRF tokens, or Authorization headers.

### Frontend

Added:

- Admin-only `UserAdministration.vue`
  - list/create user
  - role update
  - enable/disable
  - reset password
- authenticated `Account.vue`
  - current-password verified self-service password change
  - logout after successful browser password change
- routes `/admin/users` and `/account`
- admin navigation filtering plus router redirect for non-admin UI access

Backend RBAC remains the security boundary.

## Files added

- `backend/internal/api/user_admin.go`
- `backend/internal/service/user_admin.go`
- `backend/internal/service/user_admin_test.go`
- `backend/scripts/check_user_administration.py`
- `frontend/src/views/UserAdministration.vue`
- `frontend/src/views/Account.vue`
- `e2e/tests/user-administration.spec.ts`
- `docs/USER_ADMINISTRATION.md`
- `STEP7-USER-ADMINISTRATION-REPORT.md`

## Files materially updated

- `backend/internal/repository/users.go`
- `backend/internal/service/service.go`
- `backend/internal/api/router.go`
- `backend/internal/api/rbac.go`
- `backend/internal/api/rbac_test.go`
- `backend/internal/api/traceability.go`
- `backend/internal/api/openapi.json`
- `backend/scripts/check_rbac_routes.py`
- `frontend/src/api/index.ts`
- `frontend/src/router/index.ts`
- `frontend/src/App.vue`
- `.github/workflows/ci.yml`
- `Makefile`
- `README.md`
- `docs/ARCHITECTURE.md`
- `docs/BACKEND_RBAC.md`
- `e2e/README.md`
- `RELEASE-METADATA.txt`

## E2E additions

The suite now defines 23 Playwright tests. Step 7 coverage verifies:

- sole active admin demotion -> 409
- sole active admin disable -> 409
- admin creates user
- duplicate username -> 409
- non-admin cannot use `/api/admin/users` -> 403
- role change affects an already-issued token on next request
- disable affects an already-issued token on next request -> 401
- admin password reset replaces old login password
- self-service current-password change works
- user-administration audit events persist without password content
- Admin UI is visible to admin and hidden/redirected for non-admin users

## Validation completed in this environment

- Step 7 User Administration Static Guard: 70/70 PASS
- Step 6 Login Rate Limit/Auth Audit Guard: 57/57 PASS
- Step 5 Cookie Auth/CORS/CSRF Guard: 47/47 PASS
- Step 4 Production Frontend Guard: 51/51 PASS
- Step 3 Demo Credentials Guard: 41/41 PASS
- Step 2 Production Config Guard: 41/41 PASS
- 0039 Static Guard: 106 checks PASS
- 0040 Static Guard: 95/95 PASS
- 0041 Static Guard: 98/98 PASS
- All current `backend/scripts/check_*.py`: PASS
- RBAC mutation-route guard: 115 protected mutations PASS
- Runtime version policy: PASS
- Dependency lock check: PASS
- CI/dependency policy: PASS
- OpenAPI JSON parse: PASS
- Compose/GitHub Actions YAML parse: PASS
- Go changed files: `gofmt` parse/format PASS
- Step 7 TypeScript/script syntax transpilation: PASS

Step 6 baseline comparison confirms no change to:

```text
backend/internal/domain
backend/migrations
planning/recovery business services outside user administration
```

Migration state:

```text
0001-0041
count = 41
latest = 0041
0042 = absent
```

## Environment limitation

The execution environment has Go 1.23.2 while the project requires Go 1.26.x, has no network access for Go toolchain/module downloads, and has no Docker daemon. The installed Node runtime is also older than the project-pinned Node 24 runtime. Therefore these final runtime gates must execute on the development Mac or GitHub Actions:

```text
go mod verify
go vet ./...
go test -race ./...
frontend npm ci
frontend npm run typecheck
frontend npm run build
Docker production build
Playwright 23/23
```

CI includes the Step 7 static guard and the User Administration E2E test in the production-topology path.
