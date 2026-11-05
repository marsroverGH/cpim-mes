# CPIM-MES v0.41.1 Step 13 — Documentation / Final Clean-room Release Gate Report

## 1. Scope

Step 13 is the final Production Hardening stage. It does **not** add business functionality or database schema. It closes documentation drift and introduces an authoritative single-run Release Candidate promotion gate.

Candidate frozen by this source bundle:

```text
Candidate: v0.41.1-rc.2
Target GA: v0.41.1
Migration: 0001-0041 / 41 files
Migration 0042: absent
Playwright definitions: 25
```

## 2. Documentation drift corrected

The following stale documentation was corrected:

- `docs/ARCHITECTURE.md`: removed the obsolete statement that fine-grained authorization was not implemented. Current state is 51 declared permissions and 115 backend-protected business/admin mutation routes.
- `docs/CI_IMPLEMENTATION_VALIDATION.md`: replaced the old pre-lockfile bootstrap description with the current committed lockfile state (`go.sum`, frontend lockfile, E2E lockfile), normal CI, Security CI, and final RC gate.
- `e2e/README.md`: current Playwright definition count is 25 and the production Nginx/observability boundary is documented.
- `docs/0040-production-control-tower.md`: the old 17/17 count is explicitly marked as historical to the 0040 feature release rather than the current release suite.
- Root `README.md`: Step 12 and Step 13 release/security paths are documented.

New release documentation:

```text
docs/FINAL_CLEAN_ROOM_RELEASE_GATE.md
docs/RELEASE_CHECKLIST.md
RELEASE-CANDIDATE-METADATA.txt
```

## 3. Final clean-room workflow

New workflow:

```text
.github/workflows/release-candidate.yml
```

It executes:

```text
scripts/final_clean_room_release_gate.sh
```

The gate intentionally repeats critical checks in one ephemeral runner instead of treating separately successful CI jobs as sufficient release evidence.

Full sequence:

```text
source checksum verification
runtime + lock policy
Go mod verify / vet / race tests
all backend static guards
frontend typecheck/build
dependency vulnerability gate
production image build
Trivy image gate + Syft SPDX SBOM
remove Docker volumes
one-time random bootstrap admin
Production topology startup
empty DB Migration 0001 -> 0041
exact schema_migrations sequence 1..41
production port/topology validation
/version build identity
private /metrics + public 404 boundary
0041 forced FAILED-run persistence gate
Playwright 25 tests (18 baseline + 7 hardening)
live pg_dump/pg_restore drill
Step 12 security release-bundle verification
Release Candidate bundle build and recursive verification
```

## 4. Production topology gate

New checker:

```text
scripts/validate_production_topology.py
```

It rejects a merged production Compose config if:

- PostgreSQL publishes a host port;
- Backend publishes a host port;
- Frontend does not publish exactly one target port 8080.

This preserves the Step 4 deployment invariant.

## 5. Empty database migration gate

The final gate first executes:

```text
docker compose ... down -v --remove-orphans
```

Then it starts the production topology with the one-time bootstrap secret and requires `schema_migrations` to be exactly:

```text
count = 41
min   = 1
max   = 41
sequence = 1,2,...,41
```

Count/max-only validation is deliberately insufficient.

## 6. Release Candidate evidence bundle

New scripts:

```text
scripts/build_release_candidate_bundle.py
scripts/verify_release_candidate_bundle.py
scripts/test_release_candidate_unit.py
```

A successful live gate generates:

```text
release-candidate-bundle/
  cpim-mes-v0.41.1-rc.2-source.tar.gz
  security-release-bundle.tar.gz
  FINAL-CLEAN-ROOM-RESULT.json
  RELEASE-CANDIDATE-METADATA.txt
  RELEASE-CANDIDATE-METADATA.json
  RELEASE-CANDIDATE-MANIFEST.json
  RELEASE-CANDIDATE-SHA256SUMS.txt
```

The verifier recursively checks:

1. outer RC checksums and manifest;
2. clean-room status and every recorded gate is PASS;
3. source archive only contains regular files under the candidate root;
4. every source archive file matches its embedded source `SHA256SUMS.txt`;
5. nested security evidence is unpacked and re-verified by the Step 12 verifier.

The unit test also performs a 1-byte tamper and requires verification failure.

## 7. GitHub Actions supply-chain drift

Normal CI and lock-generation workflows were updated from old floating major Action references to exact release tags consistent with the Step 12 policy. Checkout credential persistence is disabled where repository code is executed.

The RC workflow uses exact releases for checkout, setup-go, setup-node, setup-python, and upload-artifact and has only `contents: read` permission.

## 8. Local validation completed in artifact-generation environment

Completed successfully:

```text
Step 13 Final Clean-room Guard         90/90 PASS
All backend check_*.py                 37/37 PASS
0039 Static Guard                      106 PASS
0040 Static Guard                      95/95 PASS
0041 Static Guard                      98/98 PASS
Step 8 FAILED Persistence              57/57 PASS
Step 9 Version Metadata                57/57 PASS
Step 10 Observability                  96/96 PASS
Step 11 Backup/Restore/DR              72/72 PASS
Step 12 Security/Supply Chain          93/93 PASS
CI/dependency policy                   PASS
DR unit tests                           4/4 PASS
Security supply-chain unit tests        PASS
Release Candidate bundle unit tests     PASS
Source checksum verification            428/428 PASS
Python syntax                           PASS
Bash syntax                             PASS
GitHub Actions YAML parse               PASS
```

Step 12 baseline byte comparison:

```text
backend/migrations          UNCHANGED
backend/internal/domain     UNCHANGED
backend/internal/service    UNCHANGED
backend/internal/repository UNCHANGED
frontend/src                UNCHANGED
e2e/tests                   UNCHANGED
backend/go.mod              UNCHANGED
backend/go.sum              UNCHANGED
frontend/package*.json      UNCHANGED
e2e/package*.json           UNCHANGED
backend/Dockerfile          UNCHANGED
frontend/Dockerfile         UNCHANGED
docker-compose.yml          UNCHANGED
docker-compose.prod.yml     UNCHANGED
```

Migration 0041 SHA-256 remains:

```text
325c558c86bd247439e7e7ea765c02b3e2e9bd718a82831aef89960f09ecdf18
```

## 9. Environment limitation / authoritative live gate

The artifact-generation environment has:

```text
Go 1.23.2
Node 22.16.0
npm 10.9.2
Docker: unavailable
```

Therefore it cannot truthfully execute the final production Docker clean-room, pinned Go 1.26.7 race suite, live vulnerability registries, Trivy image scan, Playwright 25/25, or live restore drill.

Those are not waived. They are implemented as **fail-closed mandatory checks** in `.github/workflows/release-candidate.yml`.

Accordingly:

- this archive is frozen as the **v0.41.1-rc.2 source candidate**;
- GA promotion is allowed only after the exact candidate commit passes the live Release Candidate workflow and its generated `release-candidate-bundle` verifies independently;
- any subsequent source/config/documentation modification creates a new candidate and requires the complete gate again.

## 10. Step 13 conclusion

Step 13 completes the planned Production Hardening implementation through the Release Candidate boundary while preserving:

```text
Migration latest = 0041
Migration 0042   = absent
0041 Recovery Simulation/Publish side-effect-free invariant
existing Planning/MES business semantics
```

No new application runtime dependency, business schema, or planning algorithm was introduced by Step 13.
