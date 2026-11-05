#!/usr/bin/env python3
"""CPIM-MES PostgreSQL backup, safe restore, and restore-drill tooling.

This tool deliberately operates through the private PostgreSQL Compose service so
production does not need to publish port 5432. It never prints passwords or DSNs.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import os
import re
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Iterable

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_COMPOSE_FILES = ["docker-compose.yml", "docker-compose.prod.yml"]
DB_NAME_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]{0,62}$")
EXPECTED_MIGRATION_LATEST = 41


class DRError(RuntimeError):
    pass


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def compose_base() -> list[str]:
    cmd = ["docker", "compose"]
    env_file = os.environ.get("CPIM_ENV_FILE", "").strip()
    if env_file:
        cmd += ["--env-file", env_file]
    raw_files = os.environ.get("CPIM_COMPOSE_FILES", "").strip()
    files = [p for p in raw_files.split(os.pathsep) if p] if raw_files else DEFAULT_COMPOSE_FILES
    for f in files:
        cmd += ["-f", f]
    return cmd


def safe_env() -> dict[str, str]:
    # Inherit deployment variables for docker compose interpolation without logging them.
    return os.environ.copy()


def run(cmd: list[str], *, input_bytes: bytes | None = None, capture: bool = True, check: bool = True) -> subprocess.CompletedProcess[bytes]:
    try:
        return subprocess.run(
            cmd,
            cwd=ROOT,
            env=safe_env(),
            input=input_bytes,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
            check=check,
        )
    except FileNotFoundError as exc:
        raise DRError(f"required command not found: {cmd[0]}") from exc
    except subprocess.CalledProcessError as exc:
        stderr = (exc.stderr or b"").decode("utf-8", "replace").strip()
        # Command output may contain connection details. Keep the surfaced message bounded.
        if len(stderr) > 2000:
            stderr = stderr[-2000:]
        raise DRError(f"command failed ({cmd[0]} ...): {stderr or 'no stderr'}") from exc


def compose_exec(service: str, command: Iterable[str], *, input_bytes: bytes | None = None) -> bytes:
    cp = run(compose_base() + ["exec", "-T", service, *command], input_bytes=input_bytes)
    return cp.stdout or b""


def db_user() -> str:
    return os.environ.get("POSTGRES_USER", "cpim")


def source_db() -> str:
    return os.environ.get("POSTGRES_DB", "cpim")


def validate_db_name(name: str) -> str:
    if not DB_NAME_RE.fullmatch(name):
        raise DRError("database name must match [A-Za-z_][A-Za-z0-9_]{0,62}")
    return name


def psql(db: str, sql: str, *, tuples_only: bool = True) -> str:
    cmd = ["psql", "-v", "ON_ERROR_STOP=1", "-U", db_user(), "-d", validate_db_name(db)]
    if tuples_only:
        cmd += ["-A", "-t"]
    cmd += ["-c", sql]
    return compose_exec("db", cmd).decode("utf-8", "replace").strip()


def migration_state(db: str) -> tuple[int, int]:
    out = psql(db, "SELECT count(*)::int || '|' || COALESCE(max(version),0)::int FROM schema_migrations;")
    try:
        count_s, latest_s = out.split("|", 1)
        return int(count_s), int(latest_s)
    except Exception as exc:
        raise DRError(f"unexpected schema_migrations result for {db!r}") from exc


def migration_ledger(db: str) -> str:
    return psql(db, "SELECT version || '|' || name || '|' || checksum FROM schema_migrations ORDER BY version;")


def table_row_counts(db: str) -> str:
    # psql's \\gexec executes one generated COUNT statement per public table in
    # deterministic table-name order, avoiding client-side identifier interpolation.
    script = r"""
\pset tuples_only on
\pset format unaligned
\pset fieldsep '|'
SELECT format(
  'SELECT %L, count(*)::bigint FROM %I.%I;',
  schemaname || '.' || tablename,
  schemaname,
  tablename
)
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY tablename
\gexec
""".lstrip().encode("utf-8")
    cmd = ["psql", "-v", "ON_ERROR_STOP=1", "-U", db_user(), "-d", validate_db_name(db), "-X", "-q"]
    return compose_exec("db", cmd, input_bytes=script).decode("utf-8", "replace").strip()


def database_object_inventory(db: str) -> str:
    sql = """
SELECT n.nspname || '.' || c.relname || '|' || c.relkind
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relkind IN ('r','p','v','m','S')
ORDER BY 1,2;
"""
    return psql(db, sql)


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def ensure_compose_db_running() -> str:
    version = compose_exec("db", ["pg_dump", "--version"]).decode("utf-8", "replace").strip()
    if not version:
        raise DRError("PostgreSQL db service is not reachable")
    return version


def write_secure(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    try:
        path.chmod(0o600)
    except OSError:
        pass


def verify_sidecar_checksum(archive: Path) -> None:
    sidecar = archive.with_suffix(archive.suffix + ".sha256")
    if not sidecar.exists():
        return
    fields = sidecar.read_text(encoding="utf-8").strip().split()
    if not fields:
        raise DRError(f"empty checksum sidecar: {sidecar}")
    expected = fields[0].lower()
    actual = sha256_file(archive)
    if expected != actual:
        raise DRError(f"backup checksum mismatch for {archive.name}")


def create_backup(output: Path | None = None) -> Path:
    pg_dump_version = ensure_compose_db_running()
    src = validate_db_name(source_db())
    count, latest = migration_state(src)

    if output is None:
        backup_dir = Path(os.environ.get("CPIM_BACKUP_DIR", str(ROOT / "backups")))
        if not backup_dir.is_absolute():
            backup_dir = ROOT / backup_dir
        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        output = backup_dir / f"cpim-mes-{stamp}.dump"
    output = output.expanduser().resolve()
    output.parent.mkdir(parents=True, exist_ok=True)

    fd, tmp_name = tempfile.mkstemp(prefix=f".{output.name}.", suffix=".tmp", dir=output.parent)
    os.close(fd)
    tmp = Path(tmp_name)
    try:
        cmd = compose_base() + [
            "exec", "-T", "db", "pg_dump",
            "-U", db_user(), "-d", src,
            "-Fc", "--no-owner", "--no-acl",
        ]
        with tmp.open("wb") as fh:
            proc = subprocess.run(cmd, cwd=ROOT, env=safe_env(), stdout=fh, stderr=subprocess.PIPE)
        if proc.returncode != 0:
            stderr = (proc.stderr or b"").decode("utf-8", "replace")[-2000:]
            raise DRError(f"pg_dump failed: {stderr.strip()}")
        if tmp.stat().st_size == 0:
            raise DRError("pg_dump produced an empty archive")
        try:
            tmp.chmod(0o600)
        except OSError:
            pass
        tmp.replace(output)
    finally:
        tmp.unlink(missing_ok=True)

    digest = sha256_file(output)
    checksum_path = output.with_suffix(output.suffix + ".sha256")
    manifest_path = output.with_suffix(output.suffix + ".manifest.txt")
    write_secure(checksum_path, f"{digest}  {output.name}\n")
    manifest = "\n".join([
        "CPIM-MES PostgreSQL logical backup manifest",
        f"created_at_utc={utc_now()}",
        f"source_database={src}",
        f"migration_count={count}",
        f"migration_latest={latest}",
        f"expected_migration_latest={EXPECTED_MIGRATION_LATEST}",
        f"archive_format=pg_dump_custom",
        f"archive_bytes={output.stat().st_size}",
        f"archive_sha256={digest}",
        f"pg_dump_version={pg_dump_version}",
        "contains_secrets=no_application_secret_files_but_database_contents_are_sensitive",
        "",
    ])
    write_secure(manifest_path, manifest)
    print(f"Backup created: {output}")
    print(f"SHA-256: {digest}")
    print(f"Migration ledger: count={count} latest={latest:04d}")
    return output


def database_exists(name: str) -> bool:
    validate_db_name(name)
    out = psql("postgres", f"SELECT CASE WHEN EXISTS (SELECT 1 FROM pg_database WHERE datname = '{name}') THEN 1 ELSE 0 END;")
    return out == "1"


def drop_database(name: str) -> None:
    name = validate_db_name(name)
    if name == source_db():
        raise DRError("refusing to drop the configured source database")
    # PostgreSQL 16 supports --force and this is restricted to a non-source target.
    compose_exec("db", ["dropdb", "-U", db_user(), "--if-exists", "--force", name])


def create_database(name: str) -> None:
    name = validate_db_name(name)
    compose_exec("db", ["createdb", "-U", db_user(), "-O", db_user(), name])


def restore_backup(archive: Path, target_db: str, *, replace_target: bool = False, cleanup_on_failure: bool = False) -> None:
    archive = archive.expanduser().resolve()
    if not archive.is_file() or archive.stat().st_size == 0:
        raise DRError(f"backup archive not found or empty: {archive}")
    verify_sidecar_checksum(archive)
    target = validate_db_name(target_db)
    src = validate_db_name(source_db())
    if target == src:
        raise DRError("refusing in-place restore over POSTGRES_DB; restore into a new database/instance first")
    ensure_compose_db_running()

    exists = database_exists(target)
    if exists and not replace_target:
        raise DRError(f"target database {target!r} already exists; use --replace-target only for a non-source restore target")
    if exists:
        drop_database(target)
    create_database(target)
    created = True
    container_archive = f"/tmp/cpim-restore-{os.getpid()}-{archive.name}"
    try:
        run(compose_base() + ["cp", str(archive), f"db:{container_archive}"], capture=True)
        compose_exec("db", ["pg_restore", "--list", container_archive])
        compose_exec("db", [
            "pg_restore", "--exit-on-error", "--no-owner", "--no-acl",
            "-U", db_user(), "-d", target, container_archive,
        ])
        count, latest = migration_state(target)
        print(f"Restore completed: {archive} -> database {target}")
        print(f"Restored migration ledger: count={count} latest={latest:04d}")
    except Exception:
        if cleanup_on_failure and created:
            try:
                drop_database(target)
            except Exception:
                pass
        raise
    finally:
        try:
            compose_exec("db", ["rm", "-f", container_archive])
        except Exception:
            pass


def run_drill(archive: Path | None, *, keep_backup: bool = False) -> None:
    ensure_compose_db_running()
    src = validate_db_name(source_db())
    generated = archive is None
    if archive is None:
        drill_dir = Path(os.environ.get("CPIM_BACKUP_DIR", str(ROOT / "backups")))
        if not drill_dir.is_absolute():
            drill_dir = ROOT / drill_dir
        drill_dir.mkdir(parents=True, exist_ok=True)
        archive = drill_dir / f"cpim-mes-drill-{dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')}.dump"
        create_backup(archive)
    archive = archive.expanduser().resolve()

    stamp = dt.datetime.now(dt.timezone.utc).strftime("%H%M%S")
    target = validate_db_name(f"cpim_restore_drill_{stamp}_{os.getpid()}")
    if database_exists(target):
        drop_database(target)

    try:
        source_state = migration_state(src)
        source_ledger = migration_ledger(src)
        source_counts = table_row_counts(src)
        source_inventory = database_object_inventory(src)
        restore_backup(archive, target, replace_target=False, cleanup_on_failure=True)
        restored_state = migration_state(target)
        if restored_state != source_state:
            raise DRError(f"migration state mismatch: source={source_state}, restored={restored_state}")
        if restored_state != (EXPECTED_MIGRATION_LATEST, EXPECTED_MIGRATION_LATEST):
            raise DRError(
                f"release gate requires migration count/latest {EXPECTED_MIGRATION_LATEST}; got {restored_state}"
            )
        if migration_ledger(target) != source_ledger:
            raise DRError("schema_migrations ledger differs after restore")
        if database_object_inventory(target) != source_inventory:
            raise DRError("public database object inventory differs after restore")
        restored_counts = table_row_counts(target)
        if restored_counts != source_counts:
            raise DRError("public table row counts differ after restore")
        print("Restore drill PASS")
        print(f"Source database: {src}")
        print(f"Temporary restored database: {target}")
        print(f"Migration ledger: {restored_state[0]}/{restored_state[1]:04d}")
        print("Public object inventory: MATCH")
        print("Public table row counts: MATCH")
    finally:
        try:
            drop_database(target)
        except Exception as exc:
            print(f"WARNING: failed to remove restore-drill database {target}: {exc}", file=sys.stderr)
        if generated and not keep_backup:
            for p in [archive, archive.with_suffix(archive.suffix + ".sha256"), archive.with_suffix(archive.suffix + ".manifest.txt")]:
                p.unlink(missing_ok=True)


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="CPIM-MES PostgreSQL backup / restore / restore-drill tool")
    sub = p.add_subparsers(dest="command", required=True)

    b = sub.add_parser("backup", help="create a pg_dump custom-format backup with checksum and manifest")
    b.add_argument("--output", type=Path, help="output .dump path (default: backups/cpim-mes-<UTC>.dump)")

    r = sub.add_parser("restore", help="restore into a separate target database; in-place restore is refused")
    r.add_argument("archive", type=Path)
    r.add_argument("--target-db", required=True)
    r.add_argument("--replace-target", action="store_true", help="drop/recreate an existing non-source target database")
    r.add_argument("--cleanup-on-failure", action="store_true")

    d = sub.add_parser("drill", help="restore to an ephemeral DB and compare migration ledger/object inventory/row counts")
    d.add_argument("--archive", type=Path, help="existing backup to drill; otherwise a fresh backup is created")
    d.add_argument("--keep-backup", action="store_true", help="keep a backup generated by this drill")
    return p


def main() -> int:
    args = parser().parse_args()
    try:
        if args.command == "backup":
            create_backup(args.output)
        elif args.command == "restore":
            restore_backup(args.archive, args.target_db, replace_target=args.replace_target, cleanup_on_failure=args.cleanup_on_failure)
        elif args.command == "drill":
            run_drill(args.archive, keep_backup=args.keep_backup)
        else:
            raise DRError("unsupported command")
        return 0
    except DRError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
