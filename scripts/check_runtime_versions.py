#!/usr/bin/env python3
"""Fail-closed guard for the v0.41.1 Step 1 supported runtime matrix."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

EXPECTED = {
    "go": "1.26.10",
    "go_mod": "1.26.0",
    "node": "24.20.0",
    "node_engine": "24.x",
    "npm": "11.19.0",
    "npm_engine": "11.x",
    "alpine": "3.24",
    "postgres": "16.15",
    "nginx": "1.30.6",
}

errors: list[str] = []


def text(path: str) -> str:
    p = ROOT / path
    if not p.exists():
        errors.append(f"missing required file: {path}")
        return ""
    return p.read_text(encoding="utf-8")


def require(path: str, needle: str, label: str | None = None) -> None:
    if needle not in text(path):
        errors.append(f"{path}: missing {label or needle!r}")


def package_check(path: str) -> None:
    try:
        data = json.loads(text(path))
    except Exception as exc:
        errors.append(f"{path}: invalid JSON: {exc}")
        return
    engines = data.get("engines") or {}
    if engines.get("node") != EXPECTED["node_engine"]:
        errors.append(f"{path}: engines.node must be {EXPECTED['node_engine']}")
    if engines.get("npm") != EXPECTED["npm_engine"]:
        errors.append(f"{path}: engines.npm must be {EXPECTED['npm_engine']}")
    if data.get("packageManager") != f"npm@{EXPECTED['npm']}":
        errors.append(f"{path}: packageManager must be npm@{EXPECTED['npm']}")


# Local developer/runtime selectors.
if text(".nvmrc").strip() != EXPECTED["node"]:
    errors.append(f".nvmrc: must be {EXPECTED['node']}")

mod = text("backend/go.mod")
match = re.search(r"(?m)^go\s+(\S+)\s*$", mod)
if not match or match.group(1) != EXPECTED["go_mod"]:
    errors.append(f"backend/go.mod: go directive must be {EXPECTED['go_mod']}")

# Container runtimes are patch-pinned and use a supported Alpine base.
require("backend/Dockerfile", f"FROM golang:{EXPECTED['go']}-alpine{EXPECTED['alpine']} AS build")
require("backend/Dockerfile", f"FROM alpine:{EXPECTED['alpine']}")
require("frontend/Dockerfile", f"FROM node:{EXPECTED['node']}-alpine{EXPECTED['alpine']}")
require("frontend/Dockerfile", f"npm@{EXPECTED['npm']}")
require("frontend/Dockerfile", f"FROM nginx:{EXPECTED['nginx']}-alpine{EXPECTED['alpine']} AS runtime")
require("frontend/Dockerfile.dev", f"FROM node:{EXPECTED['node']}-alpine{EXPECTED['alpine']}")
require("frontend/Dockerfile.dev", f"npm@{EXPECTED['npm']}")
require("docker-compose.yml", f"image: postgres:{EXPECTED['postgres']}-alpine{EXPECTED['alpine']}")

# npm manifests and lockfile root metadata must agree with the supported major.
for manifest in ("frontend/package.json", "e2e/package.json"):
    package_check(manifest)
for lockfile in ("frontend/package-lock.json", "e2e/package-lock.json"):
    try:
        lock = json.loads(text(lockfile))
        root_pkg = (lock.get("packages") or {}).get("", {})
        engines = root_pkg.get("engines") or {}
        if engines.get("node") != EXPECTED["node_engine"]:
            errors.append(f"{lockfile}: root engines.node must be {EXPECTED['node_engine']}")
        if engines.get("npm") != EXPECTED["npm_engine"]:
            errors.append(f"{lockfile}: root engines.npm must be {EXPECTED['npm_engine']}")
    except Exception as exc:
        errors.append(f"{lockfile}: invalid JSON: {exc}")

# CI and lock bootstrap must use the exact same patch releases.
require(".github/workflows/ci.yml", f"GO_VERSION: '{EXPECTED['go']}'")
require(".github/workflows/ci.yml", f"NODE_VERSION: '{EXPECTED['node']}'")
require(".github/workflows/ci.yml", f"NPM_VERSION: '{EXPECTED['npm']}'")
require(".github/workflows/generate-lockfiles.yml", f"go-version: '{EXPECTED['go']}'")
require(".github/workflows/generate-lockfiles.yml", f"node-version: '{EXPECTED['node']}'")
require(".github/workflows/generate-lockfiles.yml", f"npm@{EXPECTED['npm']}")
require("scripts/generate-lockfiles.sh", f'EXPECTED_GO_VERSION="go{EXPECTED["go"]}"')
require("scripts/generate-lockfiles.sh", f'EXPECTED_NODE_VERSION="v{EXPECTED["node"]}"')
require("scripts/generate-lockfiles.sh", f'EXPECTED_NPM_VERSION="{EXPECTED["npm"]}"')

if errors:
    print("Runtime version policy: FAIL")
    for error in errors:
        print(" -", error)
    sys.exit(1)

print("Runtime version policy: PASS")
print(
    "  Go {go} / Node {node} / npm {npm} / PostgreSQL {postgres} / Nginx {nginx} / Alpine {alpine}".format(
        **EXPECTED
    )
)
