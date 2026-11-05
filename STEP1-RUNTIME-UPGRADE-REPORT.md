# CPIM-MES v0.41.1 Production Hardening — Step 1 Runtime Upgrade Report

Date: 2026-08-29  
Baseline: v0.41.0 / main / `028f9e30d489148abdaf768030e21fa8e8562cb5`  
Target phase: v0.41.1 Production Hardening — Step 1 only

## Result

Step 1 Runtime Upgrade has been implemented without creating a database migration and without changing application business logic.

### Runtime matrix

| Component | v0.41.0 baseline | Step 1 baseline |
| --- | --- | --- |
| Go | 1.22 | 1.26.7 |
| Node.js | 20.x | 24.20.0 LTS |
| npm | 10.9.2 | 11.19.0 |
| PostgreSQL | 16 floating minor | 16.15 |
| Alpine runtime | 3.19 / floating Alpine tags | 3.24 |

## Files changed

- `.nvmrc`
- `backend/go.mod`
- `backend/Dockerfile`
- `frontend/Dockerfile`
- `frontend/package.json`
- `frontend/package-lock.json` (root engine metadata only)
- `e2e/package.json`
- `e2e/package-lock.json` (root engine metadata only)
- `docker-compose.yml`
- `.github/workflows/ci.yml`
- `.github/workflows/generate-lockfiles.yml`
- `scripts/generate-lockfiles.sh`
- `scripts/check_ci_dependency_policy.py`
- `scripts/check_runtime_versions.py` (new)
- `Makefile`
- `README.md`
- `docs/RUNTIME_SUPPORT.md` (new)
- `docs/CI_IMPLEMENTATION_VALIDATION.md`
- `docs/LOCKFILE_BOOTSTRAP_STATUS.md`

## Runtime enforcement added

`python3 scripts/check_runtime_versions.py` now fails closed if any of the following drift:

- Go version in CI/Docker/go.mod policy;
- Node version in `.nvmrc`, Docker, CI, manifests, or lockfile root metadata;
- npm version in packageManager/Docker/CI/lock generation;
- PostgreSQL image minor;
- Alpine base version.

`./scripts/generate-lockfiles.sh` now requires exact local versions:

```text
Go       go1.26.7
Node     v24.20.0
npm      11.19.0
```

This prevents lockfiles from being regenerated under an unsupported toolchain.

## Scope invariants preserved

- Migration range remains `0001` through `0041`.
- Migration count remains 41.
- No migration `0042` was added.
- No files under `backend/internal/`, `frontend/src/`, `e2e/tests/`, or `backend/migrations/` were changed by Step 1.
- Recovery Planning 0041 remains side-effect-free by design.
- MRP, ATP/CTP, CRP, Detailed Scheduling, Pegging, Dynamic Rescheduling, Control Tower, and Recovery Planning business semantics were not modified.
- Frontend remains the existing Vite development container; production static serving/reverse proxy is intentionally deferred to Step 4.

## Validation completed in this packaging environment

PASS:

- `python3 scripts/check_runtime_versions.py`
- `python3 scripts/check_ci_dependency_policy.py`
- `python3 scripts/check_dependency_locks.py`
- shell syntax for `scripts/generate-lockfiles.sh`
- JSON parse for Frontend/E2E manifests and lockfiles
- GitHub Actions YAML parse
- all existing `backend/scripts/check_*.py` static business-integrity guards, including:
  - 0039 Real-Time Dispatch / Dynamic Rescheduling
  - 0040 Production Control Tower
  - 0041 Scenario-Based Recovery Planning (98/98)

## Dynamic-test limitation of the packaging environment

The packaging environment cannot reach `proxy.golang.org` / npm registries and does not provide Docker. Therefore the exact upgraded runtime could not be used here to repeat:

```text
go mod tidy
go mod verify
go test ./...
go test -race ./...
go vet ./...
npm ci
npm run typecheck
npm run build
docker compose build
Playwright E2E
```

The repository is configured so those gates run with Go 1.26.7 / Node 24.20.0 / npm 11.19.0 in GitHub Actions or on an Internet-connected development Mac.

## Recommended first verification on the development Mac

```bash
cd "/Users/admin/Downloads/cpim-mes 3"

git checkout -b hardening/v0.41.1-production-hardening

# After applying/replacing with this Step 1 source:
python3 scripts/check_runtime_versions.py
npm install --global npm@11.19.0
./scripts/generate-lockfiles.sh
python3 scripts/check_dependency_locks.py

cd backend
go version
go mod tidy
go mod verify
go test ./...
go test -race ./...
go vet ./...
cd ..

cd frontend
node --version
npm --version
npm ci
npm run typecheck
npm run build
cd ..

JWT_SECRET=cpim-step1-test-secret docker compose up -d --build
```

Then run the existing clean-room Full E2E gate before committing Step 1.

## Next implementation step

After the exact-runtime build/test gate passes, proceed to **Step 2 — Production Config / Fail Closed**. No migration is required for Step 2.
