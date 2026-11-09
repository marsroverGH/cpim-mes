#!/usr/bin/env python3
"""Static guard for v0.43.0 Step 9 — Version / Build Metadata."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
checks: list[tuple[str, bool]] = []


def text(path: str) -> str:
    p = ROOT / path
    return p.read_text(encoding="utf-8") if p.exists() else ""


def check(name: str, ok: bool) -> None:
    checks.append((name, bool(ok)))


buildinfo = text("backend/internal/buildinfo/buildinfo.go")
main = text("backend/cmd/server/main.go")
router = text("backend/internal/api/router.go")
version_handler = text("backend/internal/api/version.go")
version_test = text("backend/internal/api/version_test.go")
backend_docker = text("backend/Dockerfile")
frontend_docker = text("frontend/Dockerfile")
nginx = text("frontend/nginx.conf")
prod = text("docker-compose.prod.yml")
ci = text(".github/workflows/ci.yml")
make = text("Makefile")
e2e = text("e2e/tests/version-build-metadata.spec.ts")
frontend_info = text("frontend/src/buildInfo.ts")
app = text("frontend/src/App.vue")
doc = text("docs/VERSION_BUILD_METADATA.md")

check("buildinfo package exists", "package buildinfo" in buildinfo)
check("buildinfo release default is development-marked", 'Version   = "v0.43.0-dev"' in buildinfo)
check("buildinfo commit default is unknown", 'Commit    = "unknown"' in buildinfo)
check("buildinfo build time default is unknown", 'BuildTime = "unknown"' in buildinfo)
check("buildinfo JSON version field", 'json:"version"' in buildinfo)
check("buildinfo JSON commit field", 'json:"commit"' in buildinfo)
check("buildinfo JSON buildTime field", 'json:"buildTime"' in buildinfo)
check("buildinfo JSON migrationLatest field", 'json:"migrationLatest"' in buildinfo)
check("migration latest supplied at runtime", "buildinfo.Current(result.LatestVersion)" in main)
check("startup logs build metadata", "build metadata: version=%s commit=%s build_time=%s migration_latest=%d" in main)
check("router config carries BuildInfo", "BuildInfo" in router and "buildinfo.Info" in router)
check("public root version route exists", 'r.Get("/version", srv.version)' in router)
check("version route is outside /api auth group", router.find('r.Get("/version", srv.version)') < router.find('r.Route("/api"'))
check("version response is no-store", 'Set("Cache-Control", "no-store")' in version_handler)
check("version response uses structured build info", "s.buildInfo" in version_handler)
check("version handler unit test exists", "TestVersionIsPublicAndNoStore" in version_test)
check("version handler test checks migration 42", "MigrationLatest: 45" in version_test)

for label, needle in [
    ("backend Docker release arg", "ARG RELEASE_VERSION=v0.43.0"),
    ("backend Docker commit arg", "ARG VCS_REF=unknown"),
    ("backend Docker build time arg", "ARG BUILD_TIME=unknown"),
    ("backend ldflag version", "buildinfo.Version=${RELEASE_VERSION}"),
    ("backend ldflag commit", "buildinfo.Commit=${VCS_REF}"),
    ("backend ldflag build time", "buildinfo.BuildTime=${BUILD_TIME}"),
    ("backend OCI version label", 'org.opencontainers.image.version="${RELEASE_VERSION}"'),
    ("backend OCI revision label", 'org.opencontainers.image.revision="${VCS_REF}"'),
    ("backend OCI created label", 'org.opencontainers.image.created="${BUILD_TIME}"'),
]:
    check(label, needle in backend_docker)

for label, needle in [
    ("frontend Docker release arg", "ARG RELEASE_VERSION=v0.43.0"),
    ("frontend VITE version injection", "VITE_APP_VERSION=${RELEASE_VERSION}"),
    ("frontend VITE commit injection", "VITE_APP_COMMIT=${VCS_REF}"),
    ("frontend VITE build time injection", "VITE_APP_BUILD_TIME=${BUILD_TIME}"),
    ("frontend OCI version label", 'org.opencontainers.image.version="${RELEASE_VERSION}"'),
    ("frontend OCI revision label", 'org.opencontainers.image.revision="${VCS_REF}"'),
]:
    check(label, needle in frontend_docker)

check("frontend build-info source exists", "frontendBuildInfo" in frontend_info)
check("frontend shows release version", "frontendBuildInfo.version" in app)
check("Nginx proxies root /version", "location = /version" in nginx and "proxy_pass http://backend:8080/version;" in nginx)

check("production backend receives release build args", prod.count("RELEASE_VERSION: ${RELEASE_VERSION:-v0.43.0}") >= 2)
check("production receives VCS ref build args", prod.count("VCS_REF: ${VCS_REF:-unknown}") >= 2)
check("production receives build time args", prod.count("BUILD_TIME: ${BUILD_TIME:-unknown}") >= 2)

for manifest in ["frontend/package.json", "e2e/package.json"]:
    data = json.loads(text(manifest))
    check(f"{manifest} version 0.43.0", data.get("version") == "0.43.0")
for lock in ["frontend/package-lock.json", "e2e/package-lock.json"]:
    data = json.loads(text(lock))
    check(f"{lock} root version 0.43.0", data.get("version") == "0.43.0" and data.get("packages", {}).get("", {}).get("version") == "0.43.0")

check("CI declares release version", "RELEASE_VERSION: 'v0.43.0'" in ci)
check("CI injects Git SHA", 'echo "VCS_REF=${GITHUB_SHA}"' in ci)
check("CI injects UTC build time", "date -u +'%Y-%m-%dT%H:%M:%SZ'" in ci)
check("CI runs Step 9 static guard", "check_version_build_metadata.py" in ci)
check("CI wait gate checks /version", "curl -fsS http://localhost:8080/version" in ci)
check("CI passes expected version to Playwright", "E2E_EXPECTED_VERSION" in ci)
check("CI passes expected commit to Playwright", "E2E_EXPECTED_COMMIT" in ci)
check("E2E calls public /version", "request.get('/version')" in e2e)
check("E2E checks migration latest 42", "body.migrationLatest).toBe(45)" in e2e)
check("E2E rejects unknown build time", "body.buildTime).not.toBe('unknown')" in e2e)
check("Makefile Step 9 guard target", "check-version-metadata" in make)
check("Step 9 documentation exists", "GET /version" in doc and "migrationLatest" in doc)

migrations = sorted((ROOT / "backend/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
check("migration count is 45", len(migrations) == 45)
check("latest migration is 0045", bool(migrations) and migrations[-1].name.startswith("0045_"))
check("migration 0042 present", any(p.name.startswith("0042_") for p in migrations))

failed = [name for name, ok in checks if not ok]
for name, ok in checks:
    print(("PASS" if ok else "FAIL") + ": " + name)
if failed:
    print(f"\nStep 9 Version / Build Metadata Static Guard: {len(checks)-len(failed)}/{len(checks)} PASS")
    print("FAILED: " + ", ".join(failed), file=sys.stderr)
    sys.exit(1)
print(f"\nStep 9 Version / Build Metadata Static Guard: {len(checks)}/{len(checks)} PASS")
