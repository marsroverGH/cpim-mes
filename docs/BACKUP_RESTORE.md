# Backup and Restore Operations

CPIM-MES v0.43.0 Step 11 adds an operational PostgreSQL backup/restore toolchain without exposing the production database port and without changing application schema or planning semantics.

## 1. Protection layers

Use two complementary protection layers:

1. **Logical backup / restore drill** — `pg_dump -Fc` for release validation, portable recovery, and routine restore tests.
2. **Physical base backup + WAL archive (PITR)** — the production continuity mechanism when the business RPO requires recovery between logical backup points.

A successful backup command is not proof of recoverability. Production readiness requires a restore drill on a schedule defined by the DR policy.

## 2. What the logical backup contains

`scripts/postgres_dr.py backup` runs `pg_dump` inside the private Compose `db` service using:

```text
-Fc
--no-owner
--no-acl
```

The output is a PostgreSQL custom-format archive. The tool also writes:

```text
<archive>.sha256
<archive>.manifest.txt
```

The manifest records only non-secret operational metadata:

- UTC creation time;
- source database name;
- migration count/latest version;
- archive size;
- SHA-256;
- `pg_dump` version.

The archive **contains business/database data and must be treated as sensitive**, even though application secret files, JWT secrets, and bootstrap password files are not included by `pg_dump`.

## 3. Production invocation

Production Compose needs the normal production environment for variable interpolation. Keep the populated environment file outside version control.

```bash
CPIM_ENV_FILE=.env.production ./scripts/backup_postgres.sh
```

Default output:

```text
backups/cpim-mes-YYYYMMDDTHHMMSSZ.dump
backups/cpim-mes-YYYYMMDDTHHMMSSZ.dump.sha256
backups/cpim-mes-YYYYMMDDTHHMMSSZ.dump.manifest.txt
```

Choose an explicit path when a backup agent or encrypted mount owns the destination:

```bash
CPIM_ENV_FILE=.env.production \
  ./scripts/backup_postgres.sh --output /secure-backups/cpim-mes/pre-upgrade.dump
```

The backup directory should be encrypted at rest and copied off-site/into a separate failure domain according to the DR policy. A local Docker volume or local filesystem copy alone is not a disaster-recovery copy.

## 4. Consistency

`pg_dump` takes a transactionally consistent logical snapshot. The application does not need to stop for a routine logical backup. A pre-change backup can therefore be taken while CPIM-MES is online.

For major maintenance, still record:

- `/version` output;
- backup filename and SHA-256;
- restore-drill result;
- operator/change ticket;
- intended rollback point.

## 5. Restore safety boundary

The restore tool **refuses to restore over `POSTGRES_DB`**. It always restores into a different database first.

This is deliberate:

```text
Production source DB
        │
        ├── remains untouched
        │
        └── archive -> new restore target -> verify -> controlled cutover
```

Example:

```bash
CPIM_ENV_FILE=.env.production \
  ./scripts/restore_postgres.sh backups/cpim-mes-20261105T120000Z.dump \
  --target-db cpim_restore_candidate
```

If the non-source target already exists, restore fails closed. To replace only that non-source target:

```bash
CPIM_ENV_FILE=.env.production \
  ./scripts/restore_postgres.sh backups/cpim-mes-20261105T120000Z.dump \
  --target-db cpim_restore_candidate \
  --replace-target
```

Before restoring, the tool validates the adjacent `.sha256` file when present. It copies the archive into the private DB container, runs `pg_restore --list`, then restores with:

```text
--exit-on-error
--no-owner
--no-acl
```

## 6. Automated restore drill

The restore drill is the Step 11 release gate:

```bash
CPIM_ENV_FILE=.env.production ./scripts/restore_drill.sh
```

It performs the following sequence:

```text
Source DB
  ↓
Fresh pg_dump -Fc
  ↓
Temporary restore database
  ↓
pg_restore --exit-on-error
  ↓
Compare schema_migrations ledger
  ↓
Require count=45 / latest=0045
  ↓
Compare public DB object inventory
  ↓
Compare every public table row count
  ↓
PASS
  ↓
Drop temporary restore database
```

To drill an existing archive:

```bash
CPIM_ENV_FILE=.env.production \
  ./scripts/restore_drill.sh --archive /secure-backups/cpim-mes/pre-upgrade.dump
```

A generated drill backup is removed after a successful/failed drill unless `--keep-backup` is specified.

The drill never points the application at the temporary database and never mutates the configured source database.

## 7. What a logical restore does not restore

The PostgreSQL logical archive does not contain deployment state outside PostgreSQL. Recover these independently:

- `.env.production` or secret-manager values;
- `JWT_SECRET`;
- TLS certificates/private keys;
- one-time bootstrap secret (normally it should no longer exist after bootstrap);
- reverse-proxy/load-balancer configuration outside this repository;
- external OpenTelemetry/Prometheus configuration and retained telemetry;
- off-platform artifacts or object storage if added later.

Do not store production secrets inside the database backup manifest or Git repository.

## 8. Upgrade/rollback procedure

Before a production upgrade:

```text
1. Confirm current GET /version.
2. Run logical backup.
3. Record archive SHA-256.
4. Run restore drill against that archive.
5. Confirm drill PASS.
6. Perform application upgrade.
7. Confirm /healthz and /version.
8. Confirm migration manager reports expected latest migration.
9. Execute business smoke tests.
```

If rollback requires database restoration, do not overwrite the existing database in place. Stop writes, preserve the failed/upgraded database, restore the selected backup/PITR result to a new instance/database, verify it, then perform a controlled connection cutover.

## 9. Required operational policy before go-live

The organization must explicitly approve and record:

```text
RPO
RTO
logical backup frequency
physical base-backup frequency
WAL archive retention
logical backup retention
off-site / separate-failure-domain destination
encryption mechanism and key custody
restore-drill frequency
backup alert ownership
DR incident commander / approvers
```

The repository intentionally does not invent these business requirements.

## 10. CI gate

GitHub Actions performs a live restore drill against the production Compose topology after E2E activity. The gate restores a fresh archive into an ephemeral database and compares migration ledger, public object inventory, and public-table row counts before deleting the temporary database.

This makes recoverability part of the release gate rather than a documentation-only promise.
