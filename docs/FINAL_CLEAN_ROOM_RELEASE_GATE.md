# v0.41.1 Final Clean-room Release Gate

## Purpose

Step 13 freezes the v0.41.1 Production Hardening work as a Release Candidate without changing planning semantics or database schema. The gate proves that the same candidate can start from an **empty PostgreSQL volume**, migrate `0001 -> 0041`, run the production topology, pass application/security/DR checks, and emit tamper-evident release evidence.

Target release:

```text
v0.41.1
```

Initial candidate identifier:

```text
v0.41.1-rc.8
```

Migration contract:

```text
count  = 41
latest = 0041
0042   = absent
```

## Single-run clean-room sequence

The authoritative command is:

```bash
./scripts/final_clean_room_release_gate.sh
```

It is normally executed by `.github/workflows/release-candidate.yml` on an ephemeral Ubuntu runner with the pinned runtimes.

Sequence:

```text
verify checked-in SHA-256 source manifest
    ↓
runtime + dependency-lock policy
    ↓
Go mod verify / vet / race tests
    ↓
all static guards
    ↓
frontend npm ci / typecheck / build
    ↓
govulncheck + npm audit
    ↓
production image build
    ↓
Trivy + Syft SPDX SBOM
    ↓
remove Compose volumes
    ↓
start Production topology with one-time random bootstrap admin
    ↓
Migration 0001 -> 0041
    ↓
ledger must be exactly 1..41
    ↓
production network boundary validation
    ↓
/version identity validation
    ↓
private /metrics validation
    ↓
0041 forced DB failure persistence test
    ↓
Playwright 25/25 (18 baseline + 7 hardening)
    ↓
pg_dump / pg_restore live restore drill
    ↓
security evidence bundle verification
    ↓
Release Candidate bundle build + independent verification
```

## Empty-DB requirement

The gate executes `docker compose ... down -v --remove-orphans` before production startup. The database therefore cannot inherit schema or data from a previous CI attempt. Backend startup is responsible for applying every embedded migration.

The gate then queries `schema_migrations` and requires the exact sequence `1,2,...,41`. Count/max checks alone are not sufficient.

## Production topology invariant

The merged production Compose configuration must satisfy:

```text
PostgreSQL: no host-published port
Backend:    no host-published port
Frontend:   exactly one public service, target 8080
```

The browser reaches Backend only through the production Nginx same-origin path.

## Security invariant

A Release Candidate cannot be produced unless:

- `govulncheck` contains no reachable symbol-level known vulnerability;
- npm policy gates pass;
- runtime Backend/Frontend images pass Trivy policy;
- SPDX SBOMs exist for source and both runtime images;
- the Step 12 security release bundle independently verifies.

Security-tool/network failures block the candidate.

## Recovery / observability / DR invariants

The final run also requires:

- 0041 forced simulation failure leaves durable `FAILED + failure_reason + completed_at` evidence;
- public `/metrics` is 404 while Backend-private `/metrics` is scrapeable;
- the backup/restore drill restores into an ephemeral database and verifies the migration ledger, object inventory, and public-table row counts;
- restore does not overwrite the source database.

## Release Candidate evidence

A successful run emits `release-candidate-bundle/` containing:

```text
cpim-mes-v0.41.1-rc.N-source.tar.gz
security-release-bundle.tar.gz
FINAL-CLEAN-ROOM-RESULT.json
RELEASE-CANDIDATE-METADATA.txt
RELEASE-CANDIDATE-METADATA.json
RELEASE-CANDIDATE-MANIFEST.json
RELEASE-CANDIDATE-SHA256SUMS.txt
```

The verifier opens both nested archives. It verifies source files against the source `SHA256SUMS.txt` and runs the Step 12 verifier against the nested security evidence.

## Promotion rule

Promotion from `v0.41.1-rc.N` to `v0.41.1` GA is permitted only if:

1. the final clean-room workflow is PASS;
2. the Release Candidate bundle independently verifies;
3. no source/config/documentation changes are introduced after the verified candidate.

Any code or deployment-content change after RC verification creates a new candidate and requires the full gate again.
