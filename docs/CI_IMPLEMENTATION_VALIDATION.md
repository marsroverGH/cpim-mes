# Dependency Lock / CI Implementation Validation

## Implemented

- Exact direct npm dependency versions in `frontend/package.json` and `e2e/package.json`.
- Node 20.x and npm 10.9.2 policy (`packageManager`, engines, `.nvmrc`, `.npmrc`).
- `scripts/generate-lockfiles.sh` for generating `backend/go.sum`, `frontend/package-lock.json`, and `e2e/package-lock.json` on an Internet-connected machine.
- `scripts/check_dependency_locks.py` for validating generated lockfiles against manifests.
- `scripts/check_ci_dependency_policy.py` for preventing unlocked Docker/CI regressions.
- Backend Docker build requires `go.sum`, runs `go mod verify`, and builds with `-mod=readonly`.
- Frontend Docker build requires `package-lock.json` and uses `npm ci`.
- GitHub Actions CI for lock reproducibility, Go vet/test, business-integrity guards, Vue type/build, Docker build, and Playwright E2E.
- Manual GitHub Actions lockfile bootstrap workflow.

## Validation performed in the generation environment

PASS:

- Frontend/E2E package manifest JSON parsing.
- GitHub Actions YAML parsing.
- Shell script syntax.
- CI/dependency policy guard.
- Existing RBAC, BOM integrity, final-operation, Shop Floor, PO partial receipt, ECO effective-date, and migration-manager guards.
- `gofmt` cleanliness for all backend Go source.
- Fail-closed behavior when generated lockfiles are absent (`check_dependency_locks.py`, `npm ci`, and `go test -mod=readonly` all reject the unlocked state).

## Environment limitation

The generation environment cannot reach the Go module proxy or npm registry and has no usable module/package cache. Therefore the checksum-bearing generated files are intentionally **not fabricated** in this archive:

- `backend/go.sum`
- `frontend/package-lock.json`
- `e2e/package-lock.json`

Generate them once on an Internet-connected checkout:

```bash
npm install --global npm@10.9.2
./scripts/generate-lockfiles.sh
python3 scripts/check_dependency_locks.py
```

Then commit the resulting lockfiles (and `backend/go.mod` only if `go mod tidy` legitimately changes it). After that, normal CI and Docker builds are fully lock-enforced.
