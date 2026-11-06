#!/usr/bin/env python3
"""Static guard for v0.41.1 Step 11 — Backup / Restore / Disaster Recovery."""
from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
checks: list[tuple[str, bool]] = []


def text(path: str) -> str:
    p = ROOT / path
    return p.read_text(encoding="utf-8") if p.exists() else ""


def check(name: str, ok: bool) -> None:
    checks.append((name, bool(ok)))


core = text("scripts/postgres_dr.py")
backup_sh = text("scripts/backup_postgres.sh")
restore_sh = text("scripts/restore_postgres.sh")
drill_sh = text("scripts/restore_drill.sh")
unit_test = text("scripts/test_postgres_dr_unit.py")
backup_doc = text("docs/BACKUP_RESTORE.md")
dr_doc = text("docs/DISASTER_RECOVERY.md")
ci = text(".github/workflows/ci.yml")
make = text("Makefile")
gitignore = text(".gitignore")
prod = text("docker-compose.prod.yml")
base = text("docker-compose.yml")
release = text("RELEASE-METADATA.txt")
readme = text("README.md")

check("core DR tool exists", "CPIM-MES PostgreSQL backup, safe restore, and restore-drill tooling" in core)
check("backup wrapper exists", 'postgres_dr.py" backup' in backup_sh)
check("restore wrapper exists", 'postgres_dr.py" restore' in restore_sh)
check("restore drill wrapper exists", 'postgres_dr.py" drill' in drill_sh)
check("backup uses custom pg_dump format", '"-Fc", "--no-owner", "--no-acl"' in core)
check("backup runs inside private db service", '"exec", "-T", "db", "pg_dump"' in core)
check("backup records SHA-256", "sha256_file" in core and "archive_sha256=" in core)
check("backup writes checksum sidecar", '.dump.sha256' not in core and 'with_suffix(output.suffix + ".sha256")' in core)
check("backup writes manifest", 'with_suffix(output.suffix + ".manifest.txt")' in core)
check("manifest does not claim database is non-sensitive", "database_contents_are_sensitive" in core)
check("backup temp file is atomically renamed", "tmp.replace(output)" in core)
check("backup refuses empty archive", "pg_dump produced an empty archive" in core)
check("backup captures migration count/latest", "migration_state(src)" in core and "migration_count=" in core and "migration_latest=" in core)
check("restore verifies sidecar checksum", "verify_sidecar_checksum(archive)" in core)
check("restore validates archive exists and non-empty", "backup archive not found or empty" in core)
check("restore refuses in-place source DB", "refusing in-place restore over POSTGRES_DB" in core)
check("restore target DB names are constrained", "DB_NAME_RE" in core and "database name must match" in core)
check("restore existing target fails closed by default", "already exists; use --replace-target" in core)
check("restore can only drop non-source target", "refusing to drop the configured source database" in core)
check("restore validates archive with pg_restore list", '"pg_restore", "--list"' in core)
check("restore uses exit-on-error", '"pg_restore", "--exit-on-error", "--no-owner", "--no-acl"' in core)
check("restore does not mutate app DATABASE_URL", "DATABASE_URL" not in core)
check("restore drill creates fresh backup when omitted", "generated = archive is None" in core and "create_backup(archive)" in core)
check("restore drill uses ephemeral target", "cpim_restore_drill_" in core)
check("restore drill compares migration state", "restored_state != source_state" in core)
check("restore drill requires 42/0042", "EXPECTED_MIGRATION_LATEST = 42" in core and "release gate requires migration count/latest" in core)
check("restore drill compares full migration ledger", "migration_ledger(target) != source_ledger" in core)
check("restore drill compares public object inventory", "database_object_inventory(target) != source_inventory" in core)
check("restore drill compares all public table row counts", "restored_counts != source_counts" in core)
check("restore drill cleans temporary DB", "drop_database(target)" in core)
check("restore drill generated backup cleanup", "if generated and not keep_backup" in core)
check("table counts use server-side safe identifiers", "format(" in core and "%I.%I" in core and "\\gexec" in core)
check("scripts accept production env-file indirection", "CPIM_ENV_FILE" in core)
check("scripts support compose-file override", "CPIM_COMPOSE_FILES" in core)
check("scripts do not print DATABASE_URL", "print" in core and "DATABASE_URL" not in core)
check("local backup artifacts ignored", "backups/" in gitignore and "*.dump" in gitignore)
check("Docker-free DR unit tests exist", "class PostgresDRUnitTests" in unit_test and "checksum mismatch" in unit_test and "refusing in-place restore" in unit_test)

check("backup/restore documentation exists", "# Backup and Restore Operations" in backup_doc)
check("documentation says backup is sensitive", "must be treated as sensitive" in backup_doc)
check("documentation requires separate failure domain", "separate failure domain" in backup_doc)
check("documentation shows production backup command", "./scripts/backup_postgres.sh" in backup_doc and "CPIM_ENV_FILE=.env.production" in backup_doc)
check("documentation shows restore drill", "./scripts/restore_drill.sh" in backup_doc)
check("documentation documents fail-closed restore", "refuses to restore over `POSTGRES_DB`" in backup_doc)
check("documentation lists non-DB secrets separately", "JWT_SECRET" in backup_doc and "TLS certificates" in backup_doc)
check("documentation requires policy values", "RPO" in backup_doc and "RTO" in backup_doc and "restore-drill frequency" in backup_doc)

check("DR runbook exists", "# Disaster Recovery / PITR Runbook" in dr_doc)
check("DR policy does not invent RPO", "DECIDE BEFORE GO-LIVE" in dr_doc)
check("DR covers WAL archiving", "archive_mode = on" in dr_doc and "archive_command" in dr_doc)
check("DR warns same-host WAL is insufficient", "same Docker volume" in dr_doc)
check("DR covers base backup", "pg_basebackup" in dr_doc)
check("DR covers pg_verifybackup", "pg_verifybackup" in dr_doc)
check("DR covers recovery signal", "recovery.signal" in dr_doc)
check("DR covers recovery target time", "recovery_target_time" in dr_doc)
check("DR requires new recovery instance", "new PostgreSQL instance/data directory" in dr_doc)
check("DR preserves damaged/current DB", "Preserve the damaged/current database" in dr_doc)
check("DR validates migrations 42/0042", "schema_migrations count = 42" in dr_doc and "schema_migrations latest = 0042" in dr_doc)
check("DR includes application validation", "/healthz" in dr_doc and "/version" in dr_doc)
check("DR includes restore drill evidence", "measured restore time" in dr_doc)

check("Makefile has backup target", "backup:" in make and "./scripts/backup_postgres.sh" in make)
check("Makefile has restore-drill target", "restore-drill:" in make and "./scripts/restore_drill.sh" in make)
check("Makefile has Step 11 guard", "check-backup-dr:" in make and "check_backup_restore_dr.py" in make)
check("CI runs Step 11 static guard", "check_backup_restore_dr.py" in ci)
check("CI runs Docker-free DR unit tests", "test_postgres_dr_unit.py" in ci)
check("CI runs live restore drill", "Step 11 live backup / restore drill gate" in ci and "./scripts/restore_drill.sh" in ci)
check("CI does not upload DB backup artifact", "cpim-backup" not in ci.lower() and "*.dump" not in ci)
check("README links backup docs", "docs/BACKUP_RESTORE.md" in readme and "docs/DISASTER_RECOVERY.md" in readme)
check("release metadata records Step 11", "Step 11 Backup / Restore / Disaster Recovery" in release)

check("production DB remains without published ports", "ports:" not in prod.split("  backend:", 1)[0])
check("base DB remains without published ports", "ports:" not in base.split("  backend:", 1)[0])

migrations = sorted((ROOT / "backend/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
check("migration count is 42", len(migrations) == 42)
check("latest migration is 0042", bool(migrations) and migrations[-1].name.startswith("0042_"))
check("migration 0042 present", any(p.name.startswith("0042_") for p in migrations))

failed = [name for name, ok in checks if not ok]
for name, ok in checks:
    print(("PASS" if ok else "FAIL") + ": " + name)
if failed:
    print(f"\nStep 11 Backup/Restore/DR Static Guard: {len(checks)-len(failed)}/{len(checks)} PASS")
    print("FAILED: " + ", ".join(failed), file=sys.stderr)
    sys.exit(1)
print(f"\nStep 11 Backup/Restore/DR Static Guard: {len(checks)}/{len(checks)} PASS")
