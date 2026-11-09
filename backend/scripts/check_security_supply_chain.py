#!/usr/bin/env python3
"""Step 12 static guard: Security CI, vulnerability gates, SBOM, and release evidence."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []
checks = 0


def text(path: str) -> str:
    p = ROOT / path
    if not p.exists():
        errors.append(f"missing required file: {path}")
        return ""
    return p.read_text(encoding="utf-8")


def need(path: str, needle: str, label: str | None = None) -> None:
    global checks
    checks += 1
    if needle not in text(path):
        errors.append(f"{path}: missing {label or needle!r}")


def reject(path: str, needle: str, label: str | None = None) -> None:
    global checks
    checks += 1
    if needle in text(path):
        errors.append(f"{path}: forbidden {label or needle!r}")


required = [
    ".github/workflows/security.yml",
    "scripts/security_tool_versions.env",
    "scripts/install_security_tools.sh",
    "scripts/run_dependency_security_gate.sh",
    "scripts/evaluate_govulncheck.py",
    "scripts/run_container_security_gate.sh",
    "scripts/build_release_bundle.py",
    "scripts/verify_release_bundle.py",
    "scripts/test_security_supply_chain_unit.py",
    "scripts/verify_source_checksums.py",
    "docs/SECURITY_CI_SUPPLY_CHAIN.md",
]
for path in required:
    checks += 1
    if not (ROOT / path).is_file():
        errors.append(f"missing required file: {path}")

pins = text("scripts/security_tool_versions.env")
for pattern, label in [
    (r"(?m)^GOVULNCHECK_VERSION=v1\.9\.0$", "govulncheck v1.9.0 pin"),
    (r"(?m)^TRIVY_VERSION=0\.76\.1$", "Trivy 0.76.1 pin"),
    (r"(?m)^SYFT_VERSION=1\.55\.0$", "Syft 1.55.0 pin"),
    (r"(?m)^TRIVY_CHECKSUM_FILE_SHA256=[0-9a-f]{64}$", "Trivy checksum-file SHA"),
    (r"(?m)^SYFT_CHECKSUM_FILE_SHA256=[0-9a-f]{64}$", "Syft checksum-file SHA"),
]:
    checks += 1
    if not re.search(pattern, pins):
        errors.append(f"security_tool_versions.env: missing {label}")
checks += 1
if "latest" in pins.lower():
    errors.append("security_tool_versions.env: 'latest' is forbidden")

# Installer must protect the bootstrap checksum file before trusting per-asset hashes.
for needle in [
    "--proto '=https' --tlsv1.2",
    "verify_pinned_checksum_file",
    "verify_release_asset",
    "TRIVY_CHECKSUM_FILE_SHA256",
    "SYFT_CHECKSUM_FILE_SHA256",
    'go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"',
    'export GOSUMDB="${GOSUMDB:-sum.golang.org}"',
]:
    need("scripts/install_security_tools.sh", needle)
reject("scripts/install_security_tools.sh", "@latest", "unpinned govulncheck install")

# Dependency policy.
for needle in [
    '"$BIN_DIR/govulncheck" -json ./...',
    "evaluate_govulncheck.py",
    "govulncheck-gate.json.exitcode",
    "npm audit --omit=dev --audit-level=high --json",
    "npm audit --audit-level=critical --json",
    "exit \"$status\"",
]:
    need("scripts/run_dependency_security_gate.sh", needle)
reject("scripts/run_dependency_security_gate.sh", "|| true", "unconditional audit suppression")
for needle in [
    'finding = msg.get("finding")',
    'trace = finding.get("trace")',
    'function = first.get("function")',
    '"reachableFindingCount"',
    'return 1',
]:
    need("scripts/evaluate_govulncheck.py", needle)

# Container policy and SBOM.
for needle in [
    '"spdx-json=$OUT/SBOM.spdx.json"',
    '"spdx-json=$OUT/SBOM-backend-image.spdx.json"',
    '"spdx-json=$OUT/SBOM-frontend-image.spdx.json"',
    "--severity HIGH,CRITICAL",
    "--ignore-unfixed",
    "--exit-code 1",
    "--scanners vuln",
]:
    need("scripts/run_container_security_gate.sh", needle)
reject("scripts/run_container_security_gate.sh", "--skip-db-update", "stale Trivy vulnerability DB")
checks += 1
if (ROOT / ".trivyignore").exists():
    errors.append("repository .trivyignore is forbidden for v0.43.0 supply-chain gate")

# Security workflow must be read-only and fail closed.
workflow = text(".github/workflows/security.yml")
for needle in [
    "permissions:\n  contents: read",
    "actions/checkout@v7.0.1",
    "persist-credentials: false",
    "actions/setup-go@v7.0.0",
    "cache: false",
    "actions/setup-node@v7.1.0",
    "package-manager-cache: false",
    "actions/upload-artifact@v7.0.2",
    "actions/download-artifact@v8.0.2",
    "${{ runner.temp }}/cpim-security-tools",
    "${{ runner.temp }}/cpim-security-reports",
    "${{ runner.temp }}/cpim-security-artifacts",
    "./scripts/install_security_tools.sh dependency",
    "python3 scripts/verify_source_checksums.py",
    "./scripts/run_dependency_security_gate.sh",
    "./scripts/install_security_tools.sh container",
    "./scripts/run_container_security_gate.sh",
    "python3 scripts/build_release_bundle.py",
    "python3 scripts/verify_release_bundle.py release-bundle",
    "needs: [dependency-security, container-supply-chain]",
    "retention-days: 14",
    "retention-days: 30",
]:
    checks += 1
    if needle not in workflow:
        errors.append(f"security.yml: missing {needle!r}")
checks += 1
if re.search(r"uses:\s+actions/(?:checkout|setup-go|setup-node|upload-artifact|download-artifact)@v[0-9]+(?:\s|$)", workflow):
    errors.append("security.yml: floating major-only GitHub Action tags are forbidden")
checks += 1
if re.search(r"(?m)^\s*continue-on-error:\s*true\s*$", workflow):
    errors.append("security.yml: security gates must not use continue-on-error")
checks += 1
if "pull-requests: write" in workflow or "contents: write" in workflow:
    errors.append("security.yml: write permissions are forbidden")

# Bundle builder/verifier invariants.
for needle in [
    '"releaseVersion": EXPECTED_RELEASE',
    '"migrationLatest": EXPECTED_MIGRATION_LATEST',
    '"migration0045Created": True',
    '"RELEASE-BUNDLE-SHA256SUMS.txt"',
    '"RELEASE-BUNDLE-MANIFEST.json"',
]:
    need("scripts/build_release_bundle.py", needle)
for needle in [
    "symlink is forbidden",
    "checksum mismatch",
    "manifest payload file set mismatch",
    "blocking vulnerability remains",
    "govulncheck reachable-finding gate exit code is not zero",
    "reachable symbol-level findings",
    "migration0045Created",
]:
    need("scripts/verify_release_bundle.py", needle)

# Release/documentation wiring.
need("RELEASE-METADATA.txt", "Step 12 Security CI / Supply Chain")
need("README.md", "docs/SECURITY_CI_SUPPLY_CHAIN.md")
need("Makefile", "check-security-supply-chain")
need("Makefile", "security-gate")
need("Makefile", "verify-source-checksums")
need(".github/workflows/ci.yml", "check_security_supply_chain.py")
need(".github/workflows/ci.yml", "test_security_supply_chain_unit.py")
need("scripts/check_ci_dependency_policy.py", ".github/workflows/security.yml")

# No migration or runtime dependency addition is required by Step 12.
checks += 1
migrations = sorted((ROOT / "backend/migrations").glob("*.sql"))
if len(migrations) != 45 or not migrations[-1].name.startswith("0045_"):
    errors.append(f"migration invariant failed: count={len(migrations)} latest={migrations[-1].name if migrations else 'none'}")
checks += 1
if not any(p.name.startswith("0042_") for p in migrations):
    errors.append("Migration 0042 must exist for v0.43.0")

if errors:
    print(f"Step 12 Security CI / Supply Chain Guard: FAIL ({checks} checks)")
    for error in errors:
        print(" -", error)
    sys.exit(1)

print(f"Step 12 Security CI / Supply Chain Guard: {checks}/{checks} PASS")
