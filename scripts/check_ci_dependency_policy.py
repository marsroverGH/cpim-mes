#!/usr/bin/env python3
"""Static policy guard for reproducible dependency/CI configuration.

This intentionally does not require the generated lockfiles themselves; that is
handled by check_dependency_locks.py after generation. It verifies that the
repository policy cannot silently fall back to unlocked installs/builds.
"""
from __future__ import annotations
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
errors: list[str] = []

def need(path: str, needle: str, label: str | None = None) -> None:
    text = (ROOT / path).read_text(encoding='utf-8') if (ROOT / path).exists() else ''
    if needle not in text:
        errors.append(f"{path}: missing {label or needle!r}")

def reject(path: str, needle: str, label: str | None = None) -> None:
    text = (ROOT / path).read_text(encoding='utf-8') if (ROOT / path).exists() else ''
    if needle in text:
        errors.append(f"{path}: forbidden {label or needle!r}")

required = [
    '.github/workflows/ci.yml',
    '.github/workflows/generate-lockfiles.yml',
    '.github/workflows/security.yml',
    '.github/workflows/release-candidate.yml',
    'scripts/generate-lockfiles.sh',
    'scripts/check_dependency_locks.py',
    'scripts/check_runtime_versions.py',
    'backend/Dockerfile',
    'frontend/Dockerfile',
    'frontend/Dockerfile.dev',
    'frontend/nginx.conf',
    'docker-compose.dev.yml',
    'docker-compose.prod.yml',
    '.nvmrc',
]
for f in required:
    if not (ROOT / f).exists():
        errors.append(f"missing required file: {f}")

# npm manifests must pin direct dependencies and npm itself.
for manifest in ['frontend/package.json', 'e2e/package.json']:
    p = ROOT / manifest
    if not p.exists():
        continue
    data = json.loads(p.read_text(encoding='utf-8'))
    if data.get('packageManager') != 'npm@11.19.0':
        errors.append(f"{manifest}: packageManager must be npm@11.19.0")
    for section in ('dependencies', 'devDependencies', 'optionalDependencies'):
        for name, version in data.get(section, {}).items():
            if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', str(version)):
                errors.append(f"{manifest}: {section}.{name} is not exact-pinned: {version}")

# Production builds must fail closed without lockfiles.
need('backend/Dockerfile', 'COPY go.mod go.sum ./')
need('backend/Dockerfile', 'go mod verify')
need('backend/Dockerfile', '-mod=readonly')
reject('backend/Dockerfile', 'go mod tidy', 'go mod tidy in production Docker build')
need('frontend/Dockerfile', 'COPY package.json package-lock.json ./')
need('frontend/Dockerfile', 'npm ci')
need('frontend/Dockerfile', 'npm run build')
need('frontend/Dockerfile', 'FROM nginx:')
reject('frontend/Dockerfile', 'CMD ["npm", "run", "dev"]', 'Vite dev server in production image')
reject('frontend/Dockerfile', 'npm install\n', 'unlocked npm install')
need('frontend/Dockerfile.dev', 'npm ci')
need('frontend/Dockerfile.dev', 'CMD ["npm", "run", "dev"]')

# CI must cover lock verification, tests, static guards, docker, and E2E.
for needle in [
    'check_dependency_locks.py',
    'check_runtime_versions.py',
    'git ls-files --error-unmatch',
    'go test -mod=readonly -race -count=1 ./...',
    'go mod verify',
    'npm ci',
    'docker-compose.prod.yml build --pull',
    'playwright install --with-deps chromium',
    'check_rbac_routes.py',
    'check_bom_integrity.py',
    'check_shopfloor_state_machine.py',
    'check_partial_purchase_receipts.py',
    'check_eco_effective_guard.py',
    'check_quality_transaction.py',
    'check_demo_credentials_removed.py',
    'check_production_frontend_container.py',
    'check_migration_manager.py',
    'check_version_build_metadata.py',
    'http://localhost:8080/healthz',
    'check_security_supply_chain.py',
    'test_security_supply_chain_unit.py',
    'check_final_release_candidate.py',
]:
    need('.github/workflows/ci.yml', needle)


# Step 12 security workflow must exist and preserve a read-only permission boundary.
need('.github/workflows/security.yml', 'permissions:\n  contents: read')
need('.github/workflows/security.yml', './scripts/run_dependency_security_gate.sh')
need('.github/workflows/security.yml', './scripts/run_container_security_gate.sh')
need('.github/workflows/security.yml', 'python3 scripts/verify_release_bundle.py release-bundle')
reject('.github/workflows/security.yml', 'contents: write', 'write permission in security workflow')


# Step 13 final release workflow is read-only and executes the single-run clean-room gate.
need('.github/workflows/release-candidate.yml', 'permissions:\n  contents: read')
need('.github/workflows/release-candidate.yml', './scripts/final_clean_room_release_gate.sh')
need('.github/workflows/release-candidate.yml', 'actions/checkout@v7.0.1')
need('.github/workflows/release-candidate.yml', 'persist-credentials: false')
reject('.github/workflows/release-candidate.yml', 'contents: write', 'write permission in release-candidate workflow')


if errors:
    print('CI/dependency policy: FAIL')
    for e in errors:
        print(' -', e)
    sys.exit(1)

print('CI/dependency policy: PASS')
