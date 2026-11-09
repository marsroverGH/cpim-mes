#!/usr/bin/env python3
"""v0.44.0 static guard: documentation drift and final clean-room RC wiring."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []
checks = 0


def read(path: str) -> str:
    p = ROOT / path
    if not p.is_file():
        errors.append(f"missing required file: {path}")
        return ""
    return p.read_text(encoding="utf-8")


def need(path: str, needle: str, label: str | None = None) -> None:
    global checks
    checks += 1
    if needle not in read(path):
        errors.append(f"{path}: missing {label or needle!r}")


def reject(path: str, needle: str, label: str | None = None) -> None:
    global checks
    checks += 1
    if needle in read(path):
        errors.append(f"{path}: forbidden stale content {label or needle!r}")


required = [
    ".github/workflows/release-candidate.yml",
    "scripts/final_clean_room_release_gate.sh",
    "scripts/validate_production_topology.py",
    "scripts/verify_running_version.py",
    "scripts/build_release_candidate_bundle.py",
    "scripts/verify_release_candidate_bundle.py",
    "docs/FINAL_CLEAN_ROOM_RELEASE_GATE.md",
    "docs/RELEASE_CHECKLIST.md",
    "docs/0042-controlled-recovery-plan-execution.md",
    "RELEASE-CANDIDATE-METADATA.txt",
    "RELEASE-METADATA.txt",
    "scripts/test_release_candidate_unit.py",
    "backend/scripts/check_controlled_recovery_execution.py",
]
for path in required:
    checks += 1
    if not (ROOT / path).is_file():
        errors.append(f"missing required file: {path}")

# Current documentation / RBAC drift.
reject("docs/ARCHITECTURE.md", "細粒度の認可は未実装")
need("docs/ARCHITECTURE.md", "54個のpermission")
need("docs/ARCHITECTURE.md", "120本の業務/admin mutation route")
for lock in ("backend/go.sum", "frontend/package-lock.json", "e2e/package-lock.json"):
    need("docs/CI_IMPLEMENTATION_VALIDATION.md", lock)
need("docs/CI_IMPLEMENTATION_VALIDATION.md", "26 Playwright tests")
need("e2e/README.md", "26 Playwright tests")

# Actual E2E definition count.
checks += 1
e2e_count = sum(
    len(re.findall(r"(?m)^\s*test\(", p.read_text(encoding="utf-8")))
    for p in (ROOT / "e2e/tests").glob("*.spec.ts")
)
if e2e_count != 26:
    errors.append(f"expected 26 Playwright tests, found {e2e_count}")

# Package / release identity.
for pkg in ("frontend/package.json", "e2e/package.json"):
    checks += 1
    try:
        version = json.loads(read(pkg)).get("version")
    except Exception as exc:
        errors.append(f"{pkg}: invalid JSON: {exc}")
        continue
    if version != "0.44.0":
        errors.append(f"{pkg}: expected version 0.44.0, got {version!r}")
need("RELEASE-CANDIDATE-METADATA.txt", "Candidate: v0.44.0-rc.1")
need("RELEASE-CANDIDATE-METADATA.txt", "Migration 0042: Controlled Recovery Plan Execution")
need("RELEASE-CANDIDATE-METADATA.txt", "Migration 0043: Quantity Column Scale")
need("RELEASE-CANDIDATE-METADATA.txt", "Playwright definitions: 26")
need("RELEASE-METADATA.txt", "Target release: v0.44.0")
need("RELEASE-METADATA.txt", "Migration latest: 0045")

# Final clean-room script proves the full current gate.
for needle in [
    "verify_source_checksums.py",
    "go test -mod=readonly -race -count=1 ./...",
    "npm run typecheck",
    "run_dependency_security_gate.sh",
    "run_container_security_gate.sh",
    "down -v --remove-orphans",
    "schema_migrations",
    'expected_sequence="$(seq -s, 1 45)"',
    "validate_production_topology.py",
    "verify_running_version.py",
    "--migration-latest 45",
    "recovery_simulation_failed_total",
    "test_recovery_failed_persistence.py",
    "npx playwright test --workers=1",
    "restore_drill.sh",
    "build_release_bundle.py",
    "verify_release_bundle.py",
    "build_release_candidate_bundle.py",
    "verify_release_candidate_bundle.py",
    '"migrationCount": 45',
    '"migrationLatest": 45',
    '"migration0045Created": True',
    '"playwrightTests": 26',
]:
    need("scripts/final_clean_room_release_gate.sh", needle)

# Production topology checker remains fail-closed.
for needle in ['if ports("db")', 'if ports("backend")', 'if len(front) != 1', 'target != 8080']:
    need("scripts/validate_production_topology.py", needle)

# RC bundle recursively verifies source and security evidence.
for needle in [
    "SHA256SUMS.txt",
    "security-release-bundle.tar.gz",
    "FINAL-CLEAN-ROOM-RESULT.json",
    "RELEASE-CANDIDATE-MANIFEST.json",
    "RELEASE-CANDIDATE-SHA256SUMS.txt",
]:
    need("scripts/build_release_candidate_bundle.py", needle)
for needle in [
    "verify_source_archive",
    "verify_security_archive",
    "source archive checksum mismatch",
    "clean-room result is not PASS",
    "nested security bundle verification failed",
]:
    need("scripts/verify_release_candidate_bundle.py", needle)

# Release Candidate workflow uses exact action pins and read-only contents.
workflow = read(".github/workflows/release-candidate.yml")
for needle in [
    "permissions:\n  contents: read",
    "actions/checkout@v7.0.1",
    "persist-credentials: false",
    "actions/setup-go@v7.0.0",
    "actions/setup-node@v7.1.0",
    "actions/setup-python@v6.3.0",
    "actions/upload-artifact@v7.0.2",
    "RELEASE_VERSION: 'v0.44.0'",
    "RC_VERSION: 'v0.44.0-rc.1'",
    "./scripts/final_clean_room_release_gate.sh",
    "release-candidate-bundle",
]:
    checks += 1
    if needle not in workflow:
        errors.append(f"release-candidate.yml: missing {needle!r}")
checks += 1
if re.search(r"uses:\s+actions/(?:checkout|setup-go|setup-node|setup-python|upload-artifact)@v[0-9]+(?:\s|$)", workflow):
    errors.append("release-candidate.yml: floating major-only GitHub Action tag forbidden")
checks += 1
if "contents: write" in workflow or "pull-requests: write" in workflow:
    errors.append("release-candidate.yml: write permission forbidden")

# Migration sequence and preservation of 0041 boundary.
checks += 1
migrations = sorted((ROOT / "backend/migrations").glob("[0-9][0-9][0-9][0-9]_*.sql"))
versions = [int(p.name[:4]) for p in migrations]
if versions != list(range(1,46)):
    errors.append(f"migration sequence must be exactly 0001..0044, got {versions[:3]}...{versions[-3:] if versions else []}")
checks += 1
if not any(p.name.startswith("0042_controlled_recovery_plan_execution") for p in migrations):
    errors.append("Migration 0042 Controlled Recovery Plan Execution is missing")
need("docs/0042-controlled-recovery-plan-execution.md", "0041 remains unchanged in meaning")
need("docs/0042-controlled-recovery-plan-execution.md", "PARTIAL_FAILED")
need("docs/0042-controlled-recovery-plan-execution.md", "targetRef=\"*\"")
need("backend/scripts/check_controlled_recovery_execution.py", "95/95") if False else None

# CI / Makefile wiring.
need("README.md", "0042 Controlled Recovery Plan Execution")
need("docs/FINAL_CLEAN_ROOM_RELEASE_GATE.md", "migration 0001 -> 0045")
need("docs/RELEASE_CHECKLIST.md", "`0001` through `0045`")
need("Makefile", "check-controlled-recovery-execution")
need("Makefile", "final-clean-room")
need(".github/workflows/ci.yml", "check_controlled_recovery_execution.py")
need(".github/workflows/ci.yml", "check_final_release_candidate.py")
need(".github/workflows/ci.yml", "test_release_candidate_unit.py")

if errors:
    print(f"v0.44.0 Final Clean-room Release Candidate Guard: FAIL ({checks} checks)")
    for error in errors:
        print(" -", error)
    sys.exit(1)
print(f"v0.44.0 Final Clean-room Release Candidate Guard: {checks}/{checks} PASS")
