#!/usr/bin/env python3
"""Static guard for v0.41.1 Step 3 — Demo Credentials Removal.

Migration 0003 is checksum-locked historical evidence and intentionally retains
its old comments. Runtime source, UI, API docs, E2E, Compose and current docs may
not carry the former fixed passwords.
"""
from pathlib import Path
import hashlib
import json
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
checks: list[tuple[str, bool, str]] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    checks.append((name, ok, detail))


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")

config = read("backend/internal/config/config.go")
config_test = read("backend/internal/config/config_test.go")
users = read("backend/internal/repository/users.go")
main = read("backend/cmd/server/main.go")
login = read("frontend/src/views/Login.vue")
openapi = read("backend/internal/api/openapi.json")
compose = read("docker-compose.yml")
bootstrap_compose = read("docker-compose.bootstrap.yml")
ci = read(".github/workflows/ci.yml")
e2e_creds = read("e2e/support/credentials.ts")
bootstrap_doc = read("docs/BOOTSTRAP_ADMIN.md") if (ROOT / "docs/BOOTSTRAP_ADMIN.md").exists() else ""

# Runtime implementation: no fixed-account seed path.
check("UserRepo fixed seed function removed", "SeedDefaultUsers" not in users)
check("UserRepo has atomic BootstrapAdmin", "func (r *UserRepo) BootstrapAdmin" in users)
check("bootstrap uses advisory transaction lock", "pg_advisory_xact_lock" in users and "bootstrapAdminAdvisoryLockID" in users)
check("bootstrap role is always admin", "Role:         domain.RoleAdmin" in users)
check("bootstrap only inserts into empty users table", "SELECT COUNT(*) FROM users" in users and "if count > 0" in users)
check("bootstrap password is bcrypt hashed", "bcrypt.GenerateFromPassword(plainPassword" in users)

# Config contract: no inline password / no legacy seed toggle.
check("bootstrap username config modeled", "BootstrapAdminUsername" in config)
check("bootstrap password file config modeled", "BootstrapAdminPasswordFile" in config)
check("bootstrap pair is required", "must be set together" in config)
check("legacy demo seed env is rejected", "SEED_DEMO_USERS is removed" in config)
check("inline bootstrap password env is rejected", "BOOTSTRAP_ADMIN_PASSWORD is not supported" in config)
check("password file validated before DB", "ReadBootstrapAdminPassword(cfg.BootstrapAdminPasswordFile)" in config)
check("bootstrap minimum password enforced", "BootstrapPasswordMinBytes = 15" in config)
check("bcrypt 72-byte maximum enforced", "BootstrapPasswordMaxBytes = 72" in config)
check("secret bytes zeroed during config validation", "zeroBytes(secret)" in config)

# Startup gate.
load_pos = main.find("cfg, err := config.Load()")
db_pos = main.find("repository.NewDB(cfg.DatabaseURL)")
check("config remains before DB", 0 <= load_pos < db_pos)
check("empty users table fails without bootstrap", "users table is empty; set BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD_FILE" in main)
check("startup calls BootstrapAdmin", "repos.Users.BootstrapAdmin" in main)
check("startup zeroes bootstrap password bytes", "password[i] = 0" in main)
check("startup logs no password", "log.Printf" in main and "BootstrapAdminPasswordFile" not in "\n".join(line for line in main.splitlines() if "log." in line))

# UI/API surface.
check("Login username starts empty", "const username = ref('')" in login)
check("Login password starts empty", "const password = ref('')" in login)
check("Login demo credential alert removed", "初期ユーザー" not in login)
openapi_doc = json.loads(openapi)
login_password_schema = openapi_doc["components"]["schemas"]["LoginRequest"]["properties"]["password"]
check("OpenAPI login password example removed", "example" not in login_password_schema)

# Compose and current config files.
check("base Compose has no demo seed setting", "SEED_DEMO_USERS:" not in compose)
check("bootstrap Compose mounts password as secret", "BOOTSTRAP_ADMIN_PASSWORD_FILE: /run/secrets/bootstrap_admin_password" in bootstrap_compose)
check("bootstrap Compose uses Compose secret", "secrets:" in bootstrap_compose and "file: ./.secrets/bootstrap-admin-password" in bootstrap_compose)
check("production env example has no demo seed", "SEED_DEMO_USERS=" not in read(".env.production.example"))
check("development env example has no demo seed", "SEED_DEMO_USERS=" not in read(".env.example"))

# E2E must require externally supplied credentials with no fallback.
check("E2E required-credential helper exists", "E2E_ADMIN_USERNAME" in e2e_creds and "E2E_ADMIN_PASSWORD" in e2e_creds)
check("E2E helper has no password default", "process.env.E2E_ADMIN_PASSWORD ||" not in e2e_creds and "process.env.E2E_ADMIN_PASSWORD ??" not in e2e_creds and "process.env.E2E_ADMIN_USERNAME ||" not in e2e_creds and "process.env.E2E_ADMIN_USERNAME ??" not in e2e_creds)
check("CI generates ephemeral bootstrap password", "openssl rand -base64" in ci)
check("CI masks ephemeral password", "::add-mask::" in ci)
check("CI starts production stack with bootstrap override", "docker-compose.prod.yml -f docker-compose.bootstrap.yml up -d --build" in ci)

# Documentation and migration invariants.
check("bootstrap documentation exists", "Initial Administrator Bootstrap" in bootstrap_doc)
migrations = sorted((ROOT / "backend/migrations").glob("*.sql"))
versions = [int(m.name[:4]) for m in migrations if re.match(r"^\d{4}_", m.name)]
check("migration count is 42", len(versions) == 42, f"count={len(versions)}")
check("latest migration is 0042", bool(versions) and max(versions) == 42, f"latest={max(versions) if versions else 'none'}")
check("migration 0042 present", any(v == 42 for v in versions) and not any(v > 42 for v in versions))
legacy_migration = ROOT / "backend/migrations/0003_users.sql"
legacy_sha = hashlib.sha256(legacy_migration.read_bytes()).hexdigest()
check(
    "migration 0003 checksum preserved",
    legacy_sha == "75a0b6251a0a293bed38205bb5268f82304b56f885d506ab048a355903ebac9c",
    f"sha256={legacy_sha}",
)

# Former fixed passwords may remain only in immutable migration 0003 comments.
# Derive them from the checksum-locked historical comment so this guard does not
# itself duplicate those credential literals elsewhere in the repository.
legacy_text = legacy_migration.read_text(encoding="utf-8")
forbidden = tuple(re.findall(r"[A-Za-z0-9_.-]+/([A-Za-z0-9_.-]*\d+)", legacy_text))
check("historical migration exposes four legacy password tokens for scan", len(forbidden) == 4, f"count={len(forbidden)}")
scan_roots = [
    ROOT / "backend",
    ROOT / "frontend",
    ROOT / "e2e",
    ROOT / "docs",
    ROOT / "scripts",
]
violations: list[str] = []
for base in scan_roots:
    for path in base.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(ROOT).as_posix()
        if rel == "backend/migrations/0003_users.sql":
            continue
        if path.suffix.lower() not in {".go", ".py", ".ts", ".vue", ".json", ".md", ".sql", ".sh", ".yml", ".yaml"}:
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        if any(secret in text for secret in forbidden):
            violations.append(rel)
for rel in ["README.md", ".env.example", ".env.production.example", "docker-compose.yml", "docker-compose.bootstrap.yml", "Makefile"]:
    path = ROOT / rel
    if path.exists() and any(secret in path.read_text(encoding="utf-8") for secret in forbidden):
        violations.append(rel)
check("former fixed passwords absent outside immutable migration", not violations, ", ".join(sorted(set(violations))))

failed = [x for x in checks if not x[1]]
for name, ok, detail in checks:
    suffix = f" ({detail})" if detail else ""
    print(f"{'PASS' if ok else 'FAIL'}: {name}{suffix}")
print(f"\nDemo credentials removal static guard: {len(checks)-len(failed)}/{len(checks)} PASS")
if failed:
    sys.exit(1)
