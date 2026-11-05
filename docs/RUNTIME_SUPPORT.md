# Supported Runtime Matrix — v0.41.1 Step 1

This document records the Production Hardening runtime baseline introduced after v0.41.0.
It intentionally changes infrastructure/toolchain versions only; business semantics, database schema version 0041, and Recovery Planning side-effect boundaries are unchanged.

## Pinned runtime baseline

| Component | Supported baseline | Repository enforcement |
| --- | --- | --- |
| Go | 1.26.7 | `backend/go.mod`, backend Dockerfile, GitHub Actions |
| Node.js | 24.20.0 LTS | `.nvmrc`, frontend Dockerfile, GitHub Actions |
| npm | 11.19.0 | `packageManager`, Dockerfile, CI, lock generation script |
| PostgreSQL | 16.15 | `docker-compose.yml` |
| Nginx | 1.30.4 stable | Production frontend runtime (`frontend/Dockerfile`) |
| Alpine Linux | 3.24 | Go build/runtime image, Node/Nginx image, PostgreSQL image |

`backend/go.mod` declares `go 1.26.0` as the language/toolchain floor for the 1.26 release line, while CI and container builds are patch-pinned to Go 1.26.7.

## Why these versions

- Go 1.26 remains a supported Go release line and 1.26.7 is its current patch release selected for this hardening baseline.
- Node.js 24 is LTS. Node 20 is no longer an acceptable production baseline.
- PostgreSQL remains on major version 16 to avoid a database-major upgrade during hardening, but is pinned to the current 16.x minor.
- Alpine 3.24 replaces the old Alpine 3.19 runtime base.

## Fail-closed version checks

Run:

```bash
python3 scripts/check_runtime_versions.py
```

The guard verifies that local selectors, package manifests, lockfile root metadata, Docker images, Compose, CI, and lock generation all agree with the same supported runtime matrix.

Lockfile regeneration additionally requires the exact Go, Node, and npm versions:

```bash
go version     # go1.26.7
node --version # v24.20.0
npm --version  # 11.19.0
make lock
```

## Scope boundary

Step 1 does **not**:

- add migration 0042;
- change migration 0041 or schema semantics;
- change MRP, Detailed Scheduling, Pegging, Control Tower, or Recovery Planning algorithms;
- change the Step 4 production reverse-proxy topology beyond its static Nginx boundary;
- change authentication/security behavior (Steps 2–7).
