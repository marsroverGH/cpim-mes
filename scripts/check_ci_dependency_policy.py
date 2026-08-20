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
    'scripts/generate-lockfiles.sh',
    'scripts/check_dependency_locks.py',
    'backend/Dockerfile',
    'frontend/Dockerfile',
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
    if data.get('packageManager') != 'npm@10.9.2':
        errors.append(f"{manifest}: packageManager must be npm@10.9.2")
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
reject('frontend/Dockerfile', 'npm install\n', 'unlocked npm install')

# CI must cover lock verification, tests, static guards, docker, and E2E.
for needle in [
    'check_dependency_locks.py',
    'git ls-files --error-unmatch',
    'go test -mod=readonly -race -count=1 ./...',
    'go mod verify',
    'npm ci',
    'docker compose build --pull',
    'playwright install --with-deps chromium',
    'check_rbac_routes.py',
    'check_bom_integrity.py',
    'check_shopfloor_state_machine.py',
    'check_partial_purchase_receipts.py',
    'check_eco_effective_guard.py',
    'check_quality_transaction.py',
    'check_migration_manager.py',
    'http://localhost:8080/healthz',
]:
    need('.github/workflows/ci.yml', needle)

if errors:
    print('CI/dependency policy: FAIL')
    for e in errors:
        print(' -', e)
    sys.exit(1)

print('CI/dependency policy: PASS')
