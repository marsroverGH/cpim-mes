# CPIM-MES v0.41.1 Step 3 — Demo Credentials Removal Report

Date: 2026-08-29

Baseline: `cpim-mes-v0.41.1-step2-production-config-fail-closed-complete`

Target: v0.41.1 Production Hardening — Step 3

## Result

Step 3 is implemented without adding or changing any database migration and without changing planning/domain semantics.

Fixed demo-account creation has been removed from runtime code. A clean database now requires an explicit, one-time initial administrator bootstrap using a password **file**, not an inline password environment variable.

## Implemented security contract

### Removed

- fixed `admin/planner/operator/viewer` demo account seed path
- fixed passwords from backend runtime source
- fixed credentials from Login UI
- default username/password values in Login UI
- Login UI demo-credential alert
- login password example from OpenAPI
- fixed credentials from Playwright tests
- `SEED_DEMO_USERS` as an operational configuration setting
- two obsolete historical patch artifacts that embedded old E2E login credentials

`SEED_DEMO_USERS` is now treated as a removed legacy setting: if present with a non-empty value, startup fails with upgrade guidance.

### Initial administrator bootstrap

New settings:

```text
BOOTSTRAP_ADMIN_USERNAME
BOOTSTRAP_ADMIN_PASSWORD_FILE
```

Rules:

- both settings must be configured together
- password itself is never accepted through `BOOTSTRAP_ADMIN_PASSWORD`
- password file is validated before DB network activity when bootstrap is configured
- password must be one line, 15-72 bytes
- one trailing LF/CRLF is accepted
- embedded newline/NUL is rejected
- bcrypt 72-byte input ceiling is enforced
- password contents are not logged
- plaintext byte buffers are zeroed after use

After migrations, startup checks `users`:

```text
users > 0
  -> bootstrap does not mutate existing users

users = 0 + bootstrap absent
  -> startup FAIL before HTTP listen

users = 0 + bootstrap present
  -> PostgreSQL advisory transaction lock
  -> re-check users count
  -> bcrypt hash
  -> create exactly one active admin
  -> commit
```

The advisory transaction lock makes concurrent first-start attempts idempotent at the application level.

## Docker / E2E

Added:

```text
docker-compose.bootstrap.yml
.secrets/README.md
e2e/support/credentials.ts
docs/BOOTSTRAP_ADMIN.md
backend/scripts/check_demo_credentials_removed.py
```

The one-time Compose override mounts:

```text
.secrets/bootstrap-admin-password
  -> /run/secrets/bootstrap_admin_password
```

GitHub Actions E2E now:

1. generates a fresh random bootstrap password per run;
2. masks it in Actions logs;
3. writes it to the local Compose secret file;
4. bootstraps a fresh admin;
5. passes the same ephemeral value to Playwright through `E2E_ADMIN_USERNAME` / `E2E_ADMIN_PASSWORD`;
6. removes the local secret file during teardown.

Playwright has no credential fallback.

## Migration compatibility

Migration range remains:

```text
0001 .. 0041
count = 41
latest = 0041
0042 = absent
```

`backend/migrations/0003_users.sql` is deliberately byte-for-byte unchanged. SHA-256:

```text
75a0b6251a0a293bed38205bb5268f82304b56f885d506ab048a355903ebac9c
```

Its historical comments retain the former demo credential text because changing an already-applied migration would break migration checksum compatibility. Static Guard enforces that those old password tokens appear nowhere else in current runtime source, UI, API docs, E2E, current docs, or operational configuration.

## Planning/domain invariants

Compared with Step 2, the following trees are unchanged:

```text
backend/internal/service
backend/internal/domain
backend/migrations
```

Therefore Step 3 does not modify:

- MRP
- ATP / CTP
- Detailed Scheduling
- Product Allocation
- Full Pegging
- Supplier Reliability
- Statistical Inventory Policy
- Maintenance / OEE
- Dynamic Rescheduling
- Production Control Tower
- Scenario-Based Recovery Planning

Recovery Planning remains side-effect free.

## Validation performed

### Step 3 guard

```text
Demo credentials removal static guard: 41/41 PASS
```

### Production configuration guard

```text
Production config static guard: 41/41 PASS
```

### Existing business Static Guards

All `backend/scripts/check_*.py` guards PASS, including:

```text
0039 Real-Time Dispatch / Dynamic Rescheduling: 106 checks PASS
0040 Production Control Tower:                 95/95 PASS
0041 Scenario-Based Recovery Planning:         98/98 PASS
```

### Runtime / dependency policy

```text
Runtime version policy: PASS
Dependency lock check: PASS
CI/dependency policy: PASS
```

Runtime remains Step 1 baseline:

```text
Go         1.26.10
Node.js    24.20.0 LTS
npm        11.19.0
PostgreSQL 16.15
Alpine     3.24
```

### Config unit tests

The local environment has Go 1.23 rather than the project-pinned Go 1.26.10. Because the config package uses only the Go standard library, it was tested directly in GOPATH compatibility mode:

```text
GO111MODULE=off go test -count=1
PASS
17 config tests
```

### Syntax/format checks

```text
OpenAPI JSON parse: PASS
Compose YAML parse: PASS
GitHub Actions YAML parse: PASS
TypeScript source parse: PASS
git diff --check: PASS
```

## Validation not executable in this environment

This runtime has no Docker daemon and does not have the Go module cache required for the project's external dependencies. Internet access for package retrieval is unavailable. Therefore the following final gates must run on the development Mac or GitHub Actions:

```bash
cd backend
go mod verify
go vet -mod=readonly ./...
go test -mod=readonly -race -count=1 ./...

cd ../frontend
npm ci
npm run typecheck
npm run build

cd ../e2e
npm ci
npx playwright test --workers=1
```

For a clean-room E2E database, use the bootstrap Compose override and ephemeral E2E credentials as implemented in `.github/workflows/ci.yml`.

## Next step

Step 4 — Production Frontend Container.

Step 4 should keep the Step 3 authentication/bootstrap contract unchanged while replacing the Vite development server with a production static build + reverse proxy topology.
