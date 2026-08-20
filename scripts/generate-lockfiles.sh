#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

need() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: $1 is required" >&2; exit 1; }; }
need go
need node
need npm

EXPECTED_NPM_VERSION="10.9.2"
ACTUAL_NPM_VERSION="$(npm --version)"
if [[ "$ACTUAL_NPM_VERSION" != "$EXPECTED_NPM_VERSION" ]]; then
  echo "ERROR: npm $EXPECTED_NPM_VERSION is required to generate reproducible package-lock.json files (found $ACTUAL_NPM_VERSION)." >&2
  echo "Run: npm install --global npm@$EXPECTED_NPM_VERSION" >&2
  exit 1
fi

printf '==> Go dependency lock\n'
(
  cd "$ROOT/backend"
  go mod tidy
  go mod verify
)

printf '==> Frontend dependency lock\n'
(
  cd "$ROOT/frontend"
  npm install --package-lock-only --ignore-scripts --no-audit --no-fund
  npm ci --ignore-scripts --no-audit --no-fund
)

printf '==> E2E dependency lock\n'
(
  cd "$ROOT/e2e"
  npm install --package-lock-only --ignore-scripts --no-audit --no-fund
  npm ci --ignore-scripts --no-audit --no-fund
)

printf '\nGenerated:\n'
printf '  backend/go.sum\n'
printf '  frontend/package-lock.json\n'
printf '  e2e/package-lock.json\n'
printf '\nRun ./scripts/check_dependency_locks.py before committing.\n'
