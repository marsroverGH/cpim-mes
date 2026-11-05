#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${SECURITY_REPORT_DIR:-$ROOT/security-reports}"
BIN_DIR="${SECURITY_TOOL_BIN_DIR:-$ROOT/.tools/bin}"
mkdir -p "$REPORT_DIR"

status=0

run_capture() {
  local label="$1" outfile="$2"; shift 2
  echo "==> $label"
  set +e
  "$@" >"$outfile" 2>"${outfile}.stderr"
  local rc=$?
  set -e
  printf '%s\n' "$rc" >"${outfile}.exitcode"
  if [[ $rc -ne 0 ]]; then
    echo "FAIL: $label (exit $rc)" >&2
    status=1
  else
    echo "PASS: $label"
  fi
}

if [[ ! -x "$BIN_DIR/govulncheck" ]]; then
  echo "ERROR: govulncheck not installed at $BIN_DIR/govulncheck" >&2
  exit 1
fi

# govulncheck machine-readable (-json) mode can exit 0 even when findings exist.
# Capture the stream first, then independently gate on symbol-level reachable
# findings. A network/tool error from govulncheck itself also blocks the release.
echo "==> govulncheck JSON scan"
set +e
(
  cd "$ROOT/backend"
  "$BIN_DIR/govulncheck" -json ./...
) >"$REPORT_DIR/govulncheck.json" 2>"$REPORT_DIR/govulncheck.json.stderr"
gov_rc=$?
set -e
printf '%s\n' "$gov_rc" >"$REPORT_DIR/govulncheck.json.exitcode"
if [[ $gov_rc -ne 0 ]]; then
  echo "FAIL: govulncheck command (exit $gov_rc)" >&2
  status=1
  printf '%s\n' "2" >"$REPORT_DIR/govulncheck-gate.json.exitcode"
  : >"$REPORT_DIR/govulncheck-gate.json"
  printf '%s\n' "govulncheck command failed before reachability evaluation" >"$REPORT_DIR/govulncheck-gate.json.stderr"
else
  echo "==> govulncheck reachable-finding gate"
  set +e
  python3 "$ROOT/scripts/evaluate_govulncheck.py" \
    "$REPORT_DIR/govulncheck.json" \
    --summary "$REPORT_DIR/govulncheck-gate.json" \
    >"$REPORT_DIR/govulncheck-gate.json.stdout" \
    2>"$REPORT_DIR/govulncheck-gate.json.stderr"
  gate_rc=$?
  set -e
  printf '%s\n' "$gate_rc" >"$REPORT_DIR/govulncheck-gate.json.exitcode"
  if [[ $gate_rc -ne 0 ]]; then
    echo "FAIL: govulncheck reachable vulnerability gate (exit $gate_rc)" >&2
    status=1
  else
    echo "PASS: govulncheck reachable vulnerability gate"
  fi
fi

# Runtime dependencies: HIGH/CRITICAL is a blocking release gate.
run_capture "frontend production npm audit (HIGH+)" \
  "$REPORT_DIR/npm-audit-frontend-production.json" \
  bash -lc "cd '$ROOT/frontend' && npm audit --omit=dev --audit-level=high --json"

# Build/test-only dependency trees do not ship in the runtime images, but CRITICAL
# findings still block the release because they execute inside CI/build contexts.
run_capture "frontend complete npm audit (CRITICAL)" \
  "$REPORT_DIR/npm-audit-frontend-all.json" \
  bash -lc "cd '$ROOT/frontend' && npm audit --audit-level=critical --json"
run_capture "e2e complete npm audit (CRITICAL)" \
  "$REPORT_DIR/npm-audit-e2e.json" \
  bash -lc "cd '$ROOT/e2e' && npm audit --audit-level=critical --json"

cat > "$REPORT_DIR/dependency-security-policy.txt" <<'POLICY'
CPIM-MES Step 12 dependency vulnerability policy
- govulncheck: any reachable symbol-level known Go vulnerability blocks release
- govulncheck JSON output is parsed explicitly; JSON-mode exit code alone is not trusted
- frontend production npm dependencies: HIGH or CRITICAL blocks release
- frontend complete dependency tree: CRITICAL blocks release
- E2E/build-test dependency tree: CRITICAL blocks release
- No audit result is auto-suppressed by repository policy
POLICY

exit "$status"
