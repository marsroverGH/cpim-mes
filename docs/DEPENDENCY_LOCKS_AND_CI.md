# Dependency Locks and CI

## Goals

The project uses three committed dependency locks:

- `backend/go.sum`
- `frontend/package-lock.json`
- `e2e/package-lock.json`

Direct npm dependencies are pinned to exact versions in `package.json`. CI uses `go mod verify`, `go test`, `npm ci`, Vue type/build checks, Docker Compose builds, Playwright E2E tests, and the existing business-integrity static guards.

## Generate or refresh locks

Run from the repository root on an Internet-connected development machine:

```bash
./scripts/generate-lockfiles.sh
python3 scripts/check_dependency_locks.py
```

Then review and commit all three lockfiles together with any intentional manifest changes.

A manual GitHub Actions workflow, `.github/workflows/generate-lockfiles.yml`, can also generate the three files and upload them as one artifact.

## CI policy

`.github/workflows/ci.yml` first fails if any lockfile is missing or its root manifest does not match. Backend CI then verifies `go.sum` checksums and rejects a `go mod tidy` diff. Frontend and E2E use `npm ci`, which refuses a manifest/lock mismatch.

The integration job starts PostgreSQL, Backend, and Frontend through Docker Compose, waits for health, runs Playwright, retains the HTML report, and always tears down the stack.

## Updating dependencies

Do not edit generated lockfiles by hand. Change the dependency version in `go.mod` or `package.json`, run `./scripts/generate-lockfiles.sh`, run CI locally where possible, and commit the manifest and lockfile changes together.

## First bootstrap from this generated archive

If the archive was produced in an offline analysis environment and the three generated locks are not yet present, run this once from a machine that can reach the Go module proxy and npm registry:

```bash
./scripts/generate-lockfiles.sh
python3 scripts/check_dependency_locks.py
docker compose build
```

The normal CI dependency-lock job also regenerates the files and uploads them as a workflow artifact. It then fails if the committed copies differ, so a first CI run can be used to obtain the generated files without silently accepting uncommitted dependency changes.

The production Dockerfiles intentionally require the lockfiles. There is no unlocked fallback path.
