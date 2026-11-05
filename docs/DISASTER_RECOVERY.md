# Disaster Recovery / PITR Runbook

This runbook covers CPIM-MES v0.41.1 recovery planning. It complements `BACKUP_RESTORE.md`.

## 1. Recovery objectives are business decisions

Before production go-live, record approved values for:

| Item | Approved value | Owner |
|---|---|---|
| RPO | **DECIDE BEFORE GO-LIVE** | Business + IT |
| RTO | **DECIDE BEFORE GO-LIVE** | Business + IT |
| Logical backup frequency | **DECIDE** | Operations |
| Physical base-backup frequency | **DECIDE** | DBA/Operations |
| WAL archive retention | **DECIDE** | DBA/Operations |
| Restore drill frequency | **DECIDE** | Operations |
| Off-site copy target | **DECIDE** | Security/Operations |
| Encryption/key custody | **DECIDE** | Security |

Do not claim a production RPO/RTO until a timed restore drill proves it on production-equivalent infrastructure.

## 2. Recommended protection architecture

```text
PostgreSQL 16 primary
       │
       ├── periodic pg_dump -Fc
       │       └── encrypted backup store / separate failure domain
       │
       └── WAL stream/archive
               └── durable off-host archive

Periodic physical base backup
       └── durable off-host backup store

Base backup + archived WAL
       └── Point-In-Time Recovery candidate
               └── verify
                       └── controlled application cutover
```

The logical backup is portable and easy to validate. PITR is required when the approved RPO is smaller than the logical-backup interval.

## 3. PITR prerequisites

For PostgreSQL 16 production PITR, configure and operate a real archival destination before claiming PITR capability. At minimum:

```text
archive_mode = on
archive_command = <command that durably stores %p as %f in a separate failure domain>
```

The archive command must be idempotent and must not silently overwrite a different WAL file with the same name. Monitor archival failures and archive lag.

Do **not** use a directory on the same host or the same Docker volume as the only WAL archive. That protects against neither host loss nor storage loss.

Use a dedicated backup/archive identity with the minimum privileges your backup technology needs. Do not embed long-lived cloud/object-storage credentials in the repository or Compose YAML.

## 4. Physical base backup

A production base-backup process should use PostgreSQL's physical backup tooling (for example `pg_basebackup`) and retain the generated backup manifest. Verify the completed backup with `pg_verifybackup` before marking the backup successful.

A typical operational flow is:

```text
1. Start base backup to a new unique destination.
2. Stream/include required WAL for backup consistency.
3. Complete backup.
4. Run pg_verifybackup.
5. Encrypt/upload to the approved backup store.
6. Record immutable backup ID, start/end time, checksum/manifest.
7. Apply retention policy only after the new backup is verified.
```

Exact storage commands depend on the organization's object store/backup product and are intentionally not hard-coded into CPIM-MES.

## 5. PITR recovery procedure

Use a **new PostgreSQL instance/data directory**. Never experiment on the only copy of the damaged database.

```text
1. Declare incident and freeze application writes.
2. Preserve the damaged/current database for forensic rollback.
3. Select the last verified physical base backup before the target time.
4. Restore the base backup into a new PostgreSQL 16 recovery instance.
5. Configure restore_command to retrieve archived WAL.
6. Set an explicit recovery_target_time / recovery target.
7. Create recovery.signal.
8. Start PostgreSQL and allow WAL replay to reach the target.
9. Confirm recovery stops/promotes at the intended point.
10. Validate schema_migrations and business smoke checks.
11. Point a non-production application instance at the recovered DB and validate.
12. Obtain change/incident approval.
13. Cut production DATABASE_URL to the verified recovered instance.
14. Start Backend, then Frontend/reverse proxy.
15. Confirm /healthz, /version, authentication, critical planning/read flows.
16. Resume writes only after approval.
```

For this release, a valid recovered CPIM-MES v0.41.1 database must have:

```text
schema_migrations count = 41
schema_migrations latest = 0041
```

The normal startup Migration Manager remains a safety gate: checksum mismatch, a history gap, or an unknown newer DB version must block API startup.

## 6. Logical disaster recovery path

When PITR is not required or physical recovery is unavailable, restore a verified `pg_dump -Fc` archive to a new database/instance using `scripts/restore_postgres.sh`.

Then:

```text
1. Run restore validation / restore drill checks.
2. Keep the old production DB untouched.
3. Point a non-production backend at the candidate.
4. Verify /version reports migrationLatest=41.
5. Perform critical business smoke tests.
6. Change production DATABASE_URL only after approval.
7. Preserve the pre-cutover database until incident closure.
```

## 7. Recovery validation checklist

Database:

```text
[ ] PostgreSQL starts without recovery errors
[ ] schema_migrations count/latest are correct
[ ] schema_migrations checksums match the application binary
[ ] expected tables/views/sequences exist
[ ] key row counts/reconciliation checks are plausible
[ ] no unexpected newer migration exists
```

Application:

```text
[ ] Backend /healthz passes
[ ] GET /version returns expected release/commit and migrationLatest=41
[ ] Admin authentication works
[ ] RBAC works
[ ] Inventory read path works
[ ] Sales Order read path works
[ ] Work Order read path works
[ ] Planning screens load
[ ] Recovery Planning evidence remains readable
[ ] /metrics is available only on the private backend network
```

Security/operations:

```text
[ ] Production JWT secret restored from secret manager, not backup archive
[ ] TLS configuration restored
[ ] backup archive/WAL access audited
[ ] recovery credentials rotated if exposure is suspected
[ ] temporary restore databases/data directories removed after approval
[ ] incident timeline and exact recovery point recorded
```

## 8. Restore drill evidence

Every scheduled DR exercise should retain non-sensitive evidence:

```text
backup/base-backup ID
backup SHA-256 or physical backup manifest ID
WAL range / recovery target (for PITR)
exercise start/end time
measured restore time
measured application recovery time
result of migration validation
result of smoke tests
operator/reviewer
issues and corrective actions
```

Do not retain database contents or secrets in the ticket merely to prove that a drill occurred.

## 9. Failure modes to rehearse

At minimum rehearse:

```text
accidental data deletion
bad application deployment requiring rollback
corrupted/unusable logical backup
loss of PostgreSQL host
loss of primary storage
WAL archive interruption
secret/config loss with database intact
backup store access failure
restore exceeding RTO
```

A backup strategy is production-ready only when these operational dependencies and ownership are understood, monitored, and periodically tested.
