# CPIM-MES v0.41.1 — Step 2 Production Config / Fail Closed

## Scope

Baseline: v0.41.1 Step 1 Runtime Upgrade bundle.

This step implements the production configuration startup gate from the v0.41.0 → v0.41.1 handoff. No business feature or schema migration is added.

## Implemented

- Added `APP_ENV` with `development`, `test`, `production` modes.
- Changed `config.Load` to return `(Config, error)` and validate before DB connection.
- Production has no fallback `DATABASE_URL` or `JWT_SECRET`.
- Production rejects the development JWT secret.
- Added `ALLOWED_ORIGINS` parsing and validation.
- Production defaults to no CORS and rejects any wildcard origin.
- Added `SEED_DEMO_USERS`; production defaults false and rejects true.
- Demo seed call is now conditional instead of unconditional.
- Added `COOKIE_SECURE` and `COOKIE_SAMESITE` configuration for the Step 5 cookie-auth migration.
- Production defaults `COOKIE_SECURE=true`, `COOKIE_SAMESITE=Strict`, and rejects insecure cookies.
- `SameSite=None` requires Secure in every environment.
- Router CORS middleware now consumes the validated allow-list and is omitted when the list is empty.
- Added config unit tests.
- Added `backend/scripts/check_production_config.py` static hardening guard and wired it into CI / `make ci-local`.
- Added `.env.example`, `.env.production.example`, and `docs/PRODUCTION_CONFIGURATION.md`.
- Updated README production/startup guidance.

## Startup boundary

```text
config.Load + Validate
    ↓ success only
repository.NewDB
    ↓
Migration Manager
    ↓
conditional development/test demo seed
    ↓
Services / Router / HTTP
```

Production configuration errors therefore stop the process before database network activity.

## Production fail-closed matrix

Expected startup failure with `APP_ENV=production` when:

- `DATABASE_URL` is empty/unset
- `JWT_SECRET` is empty/unset
- `JWT_SECRET=dev-secret-change-me-in-production`
- `SEED_DEMO_USERS=true`
- `ALLOWED_ORIGINS` contains `*`
- `COOKIE_SECURE=false`
- invalid `APP_ENV`, boolean, SameSite, or origin syntax is provided

Production-safe defaults:

- no CORS allow-list unless explicitly configured
- demo seed disabled
- Cookie Secure enabled
- SameSite Strict

## Preserved invariants

- Migration count remains 41.
- Latest migration remains 0041.
- Migration 0042 is not created.
- Existing MRP, CTP, Detailed Scheduling, Pegging, Control Tower, Recovery Planning, and shop-floor business semantics are unchanged by this step.
- Browser JWT transport/localStorage behavior is intentionally unchanged; Step 5 owns that transition.
- Existing demo users in a pre-existing database are not deleted; Step 3 owns demo-credential removal/bootstrap administration.

## Validation

Packaging validation should run:

```bash
python3 backend/scripts/check_production_config.py
python3 scripts/check_runtime_versions.py
python3 scripts/check_dependency_locks.py
python3 scripts/check_ci_dependency_policy.py
```

Exact-runtime Go unit/race/vet, Frontend build, Docker build and Playwright remain final CI gates.

## Packaging validation result

Validated in the artifact-building environment:

- Production config static guard: **38/38 PASS**
- Config unit tests: **12/12 PASS** using an isolated standard-library-only compatibility module
- Router CORS policy tests added: **2 tests** (executed by normal Go CI; package dependencies were not cached in the packaging environment)
- Runtime version policy: **PASS**
- Dependency lock structural check: **PASS**
- CI/dependency policy: **PASS**
- Existing backend business-integrity static guards: **all PASS**
- 0039 Real-Time Dispatch guard: **106 checks PASS**
- 0040 Production Control Tower guard: **95/95 PASS**
- 0041 Recovery Planning guard: **98/98 PASS**
- YAML / JSON / Python / shell syntax checks: **PASS**
- Business source comparison against Step 1:
  - `backend/internal/service`: unchanged
  - `backend/internal/repository`: unchanged
  - `backend/internal/domain`: unchanged
  - `backend/migrations`: unchanged
  - `frontend/src`: unchanged
  - `e2e/tests`: unchanged

Packaging environment limitations remain the same as Step 1: the installed Go is older than the pinned Go 1.26.10, external Go modules are not cached/network-accessible, and Docker is unavailable. Therefore the exact-runtime full `go test -race ./...`, `go vet ./...`, Docker build, frontend production build, and Playwright 18/18 gate must run on the development Mac or GitHub Actions.
