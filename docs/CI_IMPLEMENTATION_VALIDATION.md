# Dependency Lock / CI / Release-Gate Validation

This document reflects the **v0.43.0 Release Candidate** state. Earlier pre-lockfile bootstrap notes are obsolete.

## Locked dependency state

The repository contains and requires all three checksum-bearing lockfiles:

```text
backend/go.sum
frontend/package-lock.json
e2e/package-lock.json
```

Direct npm dependency versions are exact. Runtime/tooling policy is pinned to:

```text
Go       1.26.10
Node.js  24.20.0 LTS
npm      11.19.0
```

`frontend/package.json` and `e2e/package.json` are versioned `0.41.1`, and Docker builds have no unlocked fallback path:

- Go build uses `-mod=readonly` and `go mod verify`.
- Frontend uses `npm ci`.
- E2E uses `npm ci`.
- `scripts/check_dependency_locks.py` rejects manifest/lock drift.
- `scripts/generate-lockfiles.sh` requires the exact supported Go/Node/npm toolchain before regeneration.

## Normal CI

`.github/workflows/ci.yml` validates:

1. runtime policy and reproducible lockfiles;
2. `go mod verify`, `go vet`, and `go test -race`;
3. all business/hardening static guards;
4. frontend typecheck + production build;
5. production Docker images;
6. production-topology Playwright integration;
7. private observability boundary;
8. forced 0041 FAILED-run persistence;
9. live logical backup/restore drill.

GitHub-maintained Actions in the v0.43.0 RC workflows are exact-release pinned rather than floating major aliases. Checkout credentials are not persisted where a workflow executes repository code.

## Security CI

`.github/workflows/security.yml` separately provides the release-blocking supply-chain gates:

- `govulncheck` reachable-vulnerability evaluation;
- `npm audit` policy gates;
- Trivy runtime-image scan;
- Syft SPDX SBOMs;
- verified security evidence bundle with manifest and SHA-256 checksums.

A scanner/network/tool failure is a **failure**, not a skipped release condition.

## Final clean-room Release Candidate gate

`.github/workflows/release-candidate.yml` is the promotion gate for `v0.43.0-rc.N`. It intentionally performs the critical checks again in one clean-room run rather than inferring release readiness from separate earlier jobs.

The workflow runs `scripts/final_clean_room_release_gate.sh`, which requires:

```text
source SHA-256 verification
runtime + lock verification
Go vet / race tests
all static guards
frontend typecheck/build
dependency vulnerability gates
production image build
Trivy + SPDX SBOMs
empty Docker volume
Migration 0001 -> 0045
schema_migrations count=45, min=1, max=45, gap-free
DB + backend host-private / frontend-only public topology
/version build identity
private /metrics and public 404 boundary
forced 0041 FAILED-run persistence
all 26 Playwright tests
live pg_dump/pg_restore drill
security release-bundle verification
Release Candidate source/evidence bundle verification
```

Migration `0042` must be present as Controlled Recovery Plan Execution.

## Local generation-environment limitation

The artifact-generation environment used to prepare this source bundle does not have Docker and does not provide the pinned Go/Node toolchains or unrestricted external vulnerability registries. Therefore it can run the source/static/documentation gates, syntax checks, checksum verification, and dependency-independent unit tests, but **cannot truthfully substitute for the live clean-room GitHub gate**.

A source tree is considered an RC source candidate after local static verification. Promotion evidence is authoritative only after `.github/workflows/release-candidate.yml` reports PASS and produces a verified `release-candidate-bundle`.
