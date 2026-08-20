# Automatic Database Migration Management

The backend now owns database schema upgrades. PostgreSQL's
`/docker-entrypoint-initdb.d` mechanism is no longer used for CPIM-MES schema
migration because it only runs when the PostgreSQL data directory is empty.

## Startup gate

Before repositories, seed users, HTTP routes, or the API listener are created,
the backend executes:

1. connect to PostgreSQL;
2. acquire the global advisory lock `cpim-mes:schema-migrations`;
3. create/read `schema_migrations`;
4. validate migration names and SHA-256 checksums against the SQL embedded in the binary;
5. auto-baseline a legacy pre-manager database only when its schema fingerprints are contiguous and provable;
6. apply every pending migration in numeric order;
7. record the migration in the same database transaction as its SQL;
8. verify that the DB is at the exact migration version shipped in the binary;
9. only then start the API server.

If any step fails, the backend exits and does not accept requests.

## `schema_migrations`

The table records:

- `version`
- `name`
- SHA-256 `checksum`
- `applied_at`
- `execution_ms`
- `installed_by`
- whether the row was a legacy `baseline`

Already-applied SQL files are immutable. If a migration file is edited after it
has been applied, the checksum mismatch prevents the backend from starting.
Create a new migration instead of editing historical SQL.

## Atomic migration application

Some historical migrations (0018+) contained their own top-level `BEGIN;` /
`COMMIT;` because they were originally executed by PostgreSQL init scripts. The
migration manager strips only those standalone wrapper lines and runs the SQL
inside its own transaction. This makes the schema change and the
`schema_migrations` history row one atomic commit.

PL/pgSQL `BEGIN ... END` blocks inside functions and `DO $$` blocks are not
modified.

## Concurrency

Only one application instance may migrate at a time. A PostgreSQL advisory lock
serializes simultaneous backend starts. Other instances wait and then validate
the migration history after the first instance finishes.

## Existing databases (legacy bootstrap)

An existing database created by old Docker init scripts has no
`schema_migrations` table. On first startup with this version, the backend checks
unique schema fingerprints for migrations 0001..0025 and baselines only the
contiguous prefix it can prove exists.

The data-only migration 0014 is deliberately replay-safe and may be re-executed;
it uses `INSERT ... ON CONFLICT DO NOTHING`.

Automatic baseline is refused when:

- the 0001 schema itself is incomplete;
- a later migration fingerprint exists after an earlier required fingerprint is missing;
- an unknown/newer migration version is already recorded;
- an applied migration's checksum or name differs from the current binary.

This fail-closed behavior prevents an ambiguous or manually-skipped schema from
being silently declared current.

## New databases

The PostgreSQL container now creates only the database itself. The backend then
applies migrations 0001 through the latest version automatically. No migration
volume is mounted at `/docker-entrypoint-initdb.d`.

## Configuration

- `MIGRATION_TIMEOUT` — startup migration timeout; default `10m`.
- `MIGRATION_INSTALLED_BY` — value recorded in `schema_migrations`; Docker Compose defaults to `docker-compose-backend`.

## Operations

Normal upgrade:

```bash
docker compose up -d --build
```

Inspect migration history:

```sql
SELECT version, name, checksum, applied_at, execution_ms, installed_by, baseline
FROM schema_migrations
ORDER BY version;
```

A failed migration should be corrected by reconciling the legacy data condition
reported by that migration and restarting the backend. Do not manually insert a
fake `schema_migrations` row to bypass a failed safety migration.
