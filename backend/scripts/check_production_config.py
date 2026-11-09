#!/usr/bin/env python3
"""Static guard for v0.41.1 Production Config / Fail Closed (Step 2 + Step 3)."""
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
checks = []

def check(name: str, ok: bool, detail: str = "") -> None:
    checks.append((name, ok, detail))

config = (ROOT / "backend/internal/config/config.go").read_text()
config_test = (ROOT / "backend/internal/config/config_test.go").read_text()
main = (ROOT / "backend/cmd/server/main.go").read_text()
router = (ROOT / "backend/internal/api/router.go").read_text()
router_test = (ROOT / "backend/internal/api/router_config_test.go").read_text()
compose = (ROOT / "docker-compose.yml").read_text()
doc = (ROOT / "docs/PRODUCTION_CONFIGURATION.md").read_text()

check("APP_ENV is modeled", 'EnvironmentProduction  = "production"' in config)
check("Load returns validation error", "func Load() (Config, error)" in config)
check("production DB fallback is empty", 'databaseDefault = ""' in config)
check("production JWT fallback is empty", 'jwtDefault = ""' in config)
check("production wildcard CORS rejected", "must not contain wildcard origins in production" in config)
check("removed demo seed env is rejected", "SEED_DEMO_USERS is removed" in config)
check("inline bootstrap password is rejected", "BOOTSTRAP_ADMIN_PASSWORD is not supported" in config)
check("production insecure cookie rejected", "COOKIE_SECURE must be true in production" in config)
check("SameSite=None requires Secure", "COOKIE_SECURE must be true when COOKIE_SAMESITE=None" in config)
check("explicit production DB required", "DATABASE_URL must be explicitly set in production" in config)
check("explicit production JWT required", "JWT_SECRET must be explicitly set in production" in config)
check("development JWT sentinel rejected in production", "development default in production" in config)
check("origin list parser exists", "func splitCSV" in config)
check("origin syntax validation exists", "func validateOrigin" in config)

load_pos = main.find("cfg, err := config.Load()")
db_pos = main.find("repository.NewDB(cfg.DatabaseURL)")
check("configuration gate precedes DB connection", 0 <= load_pos < db_pos)
check("startup aborts on config failure", 'log.Fatalf("configuration validation failed; backend will not start' in main)
check("fixed demo seed path removed from startup", "SeedDefaultUsers" not in main and "cfg.SeedDemoUsers" not in main)
check("empty users table requires bootstrap", "users table is empty; set BOOTSTRAP_ADMIN_USERNAME and BOOTSTRAP_ADMIN_PASSWORD_FILE" in main)
check("router receives configured origins", bool(re.search(r"AllowedOrigins\s*:\s*cfg\.AllowedOrigins", main)))
check("router does not hard-code wildcard CORS", 'AllowedOrigins:   []string{"*"}' not in router)
check("router may omit CORS middleware", "if len(cfg.AllowedOrigins) > 0" in router)
check("router uses config allow-list", "AllowedOrigins:   cfg.AllowedOrigins" in router)
check("router CORS allow-list unit test exists", "TestRouterCORSUsesConfiguredCredentialedAllowList" in router_test)
check("router no-CORS unit test exists", "TestRouterWithoutOriginsDoesNotEnableCORS" in router_test)

check("Compose has APP_ENV", "APP_ENV: ${APP_ENV:-development}" in compose)
check("Compose has explicit development ALLOWED_ORIGINS", "ALLOWED_ORIGINS: ${ALLOWED_ORIGINS:-http://localhost:5173,http://127.0.0.1:5173}" in compose)
check("Compose has no demo seed setting", "SEED_DEMO_USERS:" not in compose)
check("Compose has COOKIE_SECURE", "COOKIE_SECURE: ${COOKIE_SECURE:-false}" in compose)
check("Compose documents fail-closed production override", "expected to fail closed" in compose)

for test_name in [
    "TestLoadProductionRequiresDatabaseURL",
    "TestLoadProductionRequiresJWTSecret",
    "TestLoadProductionRejectsDevelopmentJWTSecret",
    "TestLoadRejectsRemovedDemoSeedEnvironment",
    "TestLoadRejectsInlineBootstrapPassword",
    "TestLoadProductionRejectsWildcardOrigin",
    "TestLoadProductionDefaultsAreFailClosed",
    "TestLoadRejectsProductionCookieSecureFalse",
]:
    check(f"unit test present: {test_name}", f"func {test_name}" in config_test)

migrations = sorted((ROOT / "backend/migrations").glob("*.sql"))
versions = []
for path in migrations:
    match = re.match(r"^(\d{4})_", path.name)
    if match:
        versions.append(int(match.group(1)))
check("migration count is 45", len(versions) == 45, f"count={len(versions)}")
check("latest migration is 0045", bool(versions) and max(versions) == 45, f"latest={max(versions) if versions else 'none'}")
check("migration 0045 created", any(v == 45 for v in versions) and not any(v > 45 for v in versions))
check("production config documentation exists", "Production Configuration / Fail-Closed Startup Gate" in doc)

failed = [item for item in checks if not item[1]]
for name, ok, detail in checks:
    status = "PASS" if ok else "FAIL"
    suffix = f" ({detail})" if detail else ""
    print(f"{status}: {name}{suffix}")
print(f"\nProduction config static guard: {len(checks)-len(failed)}/{len(checks)} PASS")
if failed:
    sys.exit(1)
