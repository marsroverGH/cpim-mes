# CPIM-MES v0.41.1 Step 11 — Backup / Restore / Disaster Recovery

## 1. Scope

Step 11 hardens operational recoverability without changing CPIM/MES planning semantics, schema, or the 0041 Recovery Planning side-effect boundary.

Implemented:

- PostgreSQL logical backup through the private Compose DB service;
- SHA-256 checksum + backup manifest;
- fail-closed restore into a separate database;
- automated restore drill;
- CI live restore gate;
- production backup/restore runbook;
- PITR / WAL / base-backup disaster-recovery runbook;
- Step 11 static guard and Docker-free safety unit tests.

Not implemented automatically:

- an organization-specific object-storage provider;
- automatic WAL archive destination;
- an invented RPO/RTO or retention policy.

Those require business, security, storage, and operations decisions before production go-live.

## 2. Logical backup implementation

New files:

```text
scripts/postgres_dr.py
scripts/backup_postgres.sh
scripts/restore_postgres.sh
scripts/restore_drill.sh
scripts/test_postgres_dr_unit.py
```

Production backup:

```bash
CPIM_ENV_FILE=.env.production ./scripts/backup_postgres.sh
```

The backup runs inside the existing private `db` service:

```text
docker compose ... exec -T db pg_dump
```

It does not publish PostgreSQL port 5432.

Archive format/options:

```text
pg_dump -Fc --no-owner --no-acl
```

Outputs:

```text
*.dump
*.dump.sha256
*.dump.manifest.txt
```

Backup files are created with restrictive permissions where supported, written through a temporary file, and atomically renamed only after a non-empty dump succeeds.

The manifest records UTC timestamp, DB name, migration count/latest, archive size, SHA-256, and `pg_dump` version. It does not include application secret files, but explicitly marks database contents as sensitive.

## 3. Safe restore

Restore requires a different target database:

```bash
CPIM_ENV_FILE=.env.production \
  ./scripts/restore_postgres.sh backups/example.dump \
  --target-db cpim_restore_candidate
```

Safety properties:

- target database name is constrained to a safe PostgreSQL identifier subset;
- checksum sidecar is verified when present;
- source `POSTGRES_DB` is rejected before Docker access;
- existing target database is rejected unless `--replace-target` is explicit;
- even `--replace-target` cannot drop `POSTGRES_DB`;
- archive is inspected with `pg_restore --list`;
- restore uses `pg_restore --exit-on-error --no-owner --no-acl`;
- application `DATABASE_URL` is not rewritten by the tool.

The operational model is restore → verify → controlled cutover, not destructive in-place overwrite.

## 4. Restore drill

```bash
CPIM_ENV_FILE=.env.production ./scripts/restore_drill.sh
```

The drill:

```text
source DB
  -> fresh pg_dump -Fc
  -> ephemeral restore DB
  -> pg_restore --exit-on-error
  -> compare migration state
  -> require 41 / 0041
  -> compare exact schema_migrations ledger
  -> compare public object inventory
  -> compare every public table row count
  -> PASS
  -> drop ephemeral DB
  -> remove generated drill archive/sidecars
```

For exact source-vs-restored row-count comparison, run a production drill during a controlled quiet/read-only window or against a quiesced clone. CI runs the drill after Playwright activity when there are no concurrent writers.

## 5. CI release gate

`.github/workflows/ci.yml` now executes:

```text
Step 11 static guard
Docker-free DR unit tests
...
Production Compose integration
Playwright
Step 11 live backup / restore drill gate
```

The live gate uses the actual PostgreSQL 16.15 container and actual `pg_dump/pg_restore`. Generated database backups are not uploaded as CI artifacts and are deleted after verification.

## 6. Docker-free safety tests

`python3 scripts/test_postgres_dr_unit.py` validates:

- database-name SQL metacharacter rejection;
- checksum sidecar success/failure;
- in-place source restore rejection before Docker access;
- environment-file/Compose-file command construction without embedding DB secrets.

Local result in this implementation environment:

```text
4 tests / 4 PASS
```

## 7. Disaster recovery / PITR

Added:

```text
docs/BACKUP_RESTORE.md
docs/DISASTER_RECOVERY.md
```

The PITR runbook covers:

- physical base backup;
- `pg_basebackup`;
- `pg_verifybackup`;
- durable off-host WAL archive;
- `archive_mode` / `archive_command` responsibilities;
- explicit recovery target;
- `recovery.signal`;
- validation on a new recovery instance;
- migration history verification;
- non-production validation before cutover;
- controlled production `DATABASE_URL` cutover;
- DR evidence and rehearsal scenarios.

The repository intentionally does not enable a fake/local-only WAL archive. A same-host directory or same Docker volume is not accepted as the only disaster-recovery copy.

## 8. RPO / RTO policy boundary

Step 11 does not invent business recovery objectives. Before go-live the organization must approve:

```text
RPO
RTO
logical backup frequency
physical base-backup frequency
WAL archive retention
logical backup retention
off-site destination
encryption/key custody
restore-drill frequency
alert ownership
incident approval chain
```

The DR document marks these as `DECIDE BEFORE GO-LIVE` rather than silently assuming values.

## 9. Production topology invariants

Step 10 → Step 11 comparison confirms unchanged:

```text
backend/migrations
backend/internal/domain
backend/internal/service
backend/internal/repository
frontend/src
e2e/tests
backend/go.mod
backend/go.sum
frontend/package-lock.json
e2e/package-lock.json
docker-compose.yml
docker-compose.prod.yml
frontend/nginx.conf
```

Therefore Step 11 does not expose the DB port and adds no application dependency.

## 10. Migration invariants

```text
Migration count  = 41
Latest migration = 0041
Migration 0042   = absent
```

0041 SHA-256 remains:

```text
325c558c86bd247439e7e7ea765c02b3e2e9bd718a82831aef89960f09ecdf18
```

## 11. Static validation

Step 11 Static Guard:

```text
72/72 PASS
```

All `backend/scripts/check_*.py` guards also pass, including:

```text
0039 Real-Time Dispatch / Dynamic Rescheduling  106 PASS
0040 Production Control Tower                   95/95 PASS
0041 Scenario-Based Recovery Planning           98/98 PASS
Step 8 FAILED Run Persistence                   57/57 PASS
Step 9 Version / Build Metadata                 57/57 PASS
Step 10 Observability                           96/96 PASS
```

Also passed locally:

```text
Runtime version policy
Dependency lock policy
CI/dependency policy
Python syntax
Bash syntax
GitHub Actions YAML parse
Docker-free DR unit tests 4/4
```

## 12. Environment limitation

The implementation environment has:

```text
Docker: unavailable
Go:     1.23.2
Node:   22.16.0
npm:    10.9.2
```

The project runtime remains Go 1.26.10 / Node 24.20.0 / npm 11.19.0 / PostgreSQL 16.15.

Because Docker is unavailable here, the actual live `pg_dump -> pg_restore` drill cannot be executed in this container. The live restore drill is wired into GitHub Actions against the production Compose topology and is therefore a required release gate. The final production gate must also run the existing Go/Frontend/Docker/Playwright checks under the project-pinned runtimes.

## 13. Conclusion

Step 11 changes CPIM-MES from "backup instructions exist" to a verifiable recovery workflow:

```text
Backup
  -> checksum/manifest
  -> restore into isolated target
  -> migration/object/data validation
  -> release-gated restore drill
  -> controlled cutover
```

The next planned hardening step is Step 12 — Security CI / Supply Chain.
