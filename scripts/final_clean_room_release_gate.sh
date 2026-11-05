#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RELEASE_VERSION="${RELEASE_VERSION:-v0.41.1}"
RC_VERSION="${RC_VERSION:-v0.41.1-rc.4}"
VCS_REF="${VCS_REF:-${GITHUB_SHA:-local}}"
BUILD_TIME="${BUILD_TIME:-$(date -u +'%Y-%m-%dT%H:%M:%SZ')}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-rc-db-$(openssl rand -hex 16)}"
JWT_SECRET="${JWT_SECRET:-rc-jwt-$(openssl rand -hex 32)}"
DATABASE_URL="${DATABASE_URL:-postgres://cpim:${POSTGRES_PASSWORD}@db:5432/cpim?sslmode=disable}"
CPIM_HTTP_PORT="${CPIM_HTTP_PORT:-8080}"
E2E_BASE_URL="${E2E_BASE_URL:-http://localhost:${CPIM_HTTP_PORT}}"
EVIDENCE_DIR="${RC_EVIDENCE_DIR:-${RUNNER_TEMP:-/tmp}/cpim-mes-rc-evidence}"
SECURITY_REPORT_DIR="${SECURITY_REPORT_DIR:-$EVIDENCE_DIR/security-reports}"
SECURITY_ARTIFACT_DIR="${SECURITY_ARTIFACT_DIR:-$EVIDENCE_DIR/security-artifacts}"
SECURITY_TOOL_BIN_DIR="${SECURITY_TOOL_BIN_DIR:-$EVIDENCE_DIR/security-tools}"
SECURITY_BUNDLE_DIR="$EVIDENCE_DIR/security-release-bundle"
RC_BUNDLE_DIR="${RC_BUNDLE_DIR:-$ROOT/release-candidate-bundle}"
COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.prod.yml)
COMPOSE_BOOTSTRAP=(docker compose -f docker-compose.yml -f docker-compose.prod.yml -f docker-compose.bootstrap.yml)

export RELEASE_VERSION RC_VERSION VCS_REF BUILD_TIME POSTGRES_PASSWORD JWT_SECRET DATABASE_URL CPIM_HTTP_PORT E2E_BASE_URL
export SECURITY_REPORT_DIR SECURITY_ARTIFACT_DIR SECURITY_TOOL_BIN_DIR

fail() { echo "FINAL CLEAN-ROOM GATE: FAIL - $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "required command missing: $1"; }
for cmd in docker go node npm python3 curl openssl; do need "$cmd"; done
docker compose version >/dev/null 2>&1 || fail "docker compose plugin unavailable"

cleanup() {
  rc=$?
  set +e
  if [[ $rc -ne 0 ]] && command -v docker >/dev/null 2>&1; then
    mkdir -p "$EVIDENCE_DIR"
    "${COMPOSE[@]}" ps > "$EVIDENCE_DIR/docker-compose-ps.txt" 2>&1 || true
    "${COMPOSE[@]}" logs --no-color > "$EVIDENCE_DIR/docker-compose-logs.txt" 2>&1 || true
  fi
  BOOTSTRAP_ADMIN_USERNAME="${E2E_ADMIN_USERNAME:-rc-admin}" "${COMPOSE_BOOTSTRAP[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f .secrets/bootstrap-admin-password
  return $rc
}
trap cleanup EXIT

rm -rf "$EVIDENCE_DIR" "$RC_BUNDLE_DIR"
mkdir -p "$EVIDENCE_DIR" "$SECURITY_REPORT_DIR" "$SECURITY_ARTIFACT_DIR" .secrets
rm -f .secrets/bootstrap-admin-password

printf '%s\n' "==> Step 13 source/static gate"
python3 scripts/verify_source_checksums.py
python3 scripts/check_runtime_versions.py
python3 scripts/check_dependency_locks.py
python3 scripts/check_ci_dependency_policy.py
python3 backend/scripts/check_final_release_candidate.py
python3 scripts/test_postgres_dr_unit.py
python3 scripts/test_security_supply_chain_unit.py

printf '%s\n' "==> Backend verification"
(
  cd backend
  go mod verify
  go vet -mod=readonly ./...
  go test -mod=readonly -race -count=1 ./...
)

printf '%s\n' "==> All static guards"
while IFS= read -r guard; do
  python3 "$guard"
done < <(find backend/scripts -maxdepth 1 -type f -name 'check_*.py' | sort)

printf '%s\n' "==> Dependency security gate"
./scripts/install_security_tools.sh dependency
./scripts/run_dependency_security_gate.sh

printf '%s\n' "==> Empty-volume production image build"
"${COMPOSE_BOOTSTRAP[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
"${COMPOSE[@]}" build --pull
BACKEND_IMAGE_ID="$("${COMPOSE[@]}" images -q backend | head -n1)"
FRONTEND_IMAGE_ID="$("${COMPOSE[@]}" images -q frontend | head -n1)"
[[ -n "$BACKEND_IMAGE_ID" && -n "$FRONTEND_IMAGE_ID" ]] || fail "could not resolve built production image IDs"

printf '%s\n' "==> Container scan + SBOM"
./scripts/install_security_tools.sh container
BACKEND_IMAGE="$BACKEND_IMAGE_ID" FRONTEND_IMAGE="$FRONTEND_IMAGE_ID" ./scripts/run_container_security_gate.sh

printf '%s\n' "==> Frontend locked build"
(
  cd frontend
  npm ci
  npm run typecheck
  npm run build
)

printf '%s\n' "==> E2E locked install"
(
  cd e2e
  npm ci
  npx playwright install --with-deps chromium
)

printf '%s\n' "==> Prepare one-time bootstrap administrator"
password="$(openssl rand -base64 36 | tr -d '\n')"
printf '%s' "$password" > .secrets/bootstrap-admin-password
chmod 600 .secrets/bootstrap-admin-password
if [[ -n "${GITHUB_ACTIONS:-}" ]]; then echo "::add-mask::$password"; fi
E2E_ADMIN_USERNAME="${E2E_ADMIN_USERNAME:-rc-admin-${GITHUB_RUN_ID:-local}}"
E2E_ADMIN_PASSWORD="$password"
export E2E_ADMIN_USERNAME E2E_ADMIN_PASSWORD

printf '%s\n' "==> Start production topology from empty DB volume"
BOOTSTRAP_ADMIN_USERNAME="$E2E_ADMIN_USERNAME" "${COMPOSE_BOOTSTRAP[@]}" up -d

printf '%s\n' "==> Wait for production topology"
ready=0
for _ in $(seq 1 120); do
  if curl -fsS "$E2E_BASE_URL/healthz" >/dev/null 2>&1 \
    && curl -fsS "$E2E_BASE_URL/version" >/dev/null 2>&1 \
    && curl -fsS "$E2E_BASE_URL/api/openapi.json" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" != 1 ]]; then
  "${COMPOSE[@]}" ps || true
  "${COMPOSE[@]}" logs --no-color || true
  fail "production topology did not become ready"
fi

printf '%s\n' "==> Production topology host-port boundary"
python3 scripts/validate_production_topology.py < <("${COMPOSE[@]}" config --format json)

printf '%s\n' "==> Empty DB migration ledger 0001 -> 0041"
ledger="$("${COMPOSE[@]}" exec -T db psql -U cpim -d cpim -Atqc \
  "SELECT count(*)::text || ':' || min(version)::text || ':' || max(version)::text || ':' || string_agg(version::text, ',' ORDER BY version) FROM schema_migrations;")"
expected_sequence="$(seq -s, 1 41)"
[[ "$ledger" == "41:1:41:${expected_sequence}" ]] || fail "unexpected migration ledger: $ledger"

printf '%s\n' "==> Running build identity"
python3 scripts/verify_running_version.py \
  --url "$E2E_BASE_URL/version" \
  --version "$RELEASE_VERSION" \
  --commit "$VCS_REF" \
  --migration-latest 41

printf '%s\n' "==> Private observability boundary"
"${COMPOSE[@]}" exec -T backend wget -qO- http://127.0.0.1:8080/metrics > "$EVIDENCE_DIR/metrics.txt"
grep -q '^# HELP http_requests_total ' "$EVIDENCE_DIR/metrics.txt"
grep -q '^# HELP db_open_connections ' "$EVIDENCE_DIR/metrics.txt"
grep -q '^# HELP mrp_runs_total ' "$EVIDENCE_DIR/metrics.txt"
grep -q '^# HELP recovery_simulation_failed_total ' "$EVIDENCE_DIR/metrics.txt"
public_metrics_status="$(curl -sS -o /dev/null -w '%{http_code}' "$E2E_BASE_URL/metrics")"
[[ "$public_metrics_status" == "404" ]] || fail "public /metrics must be 404, got $public_metrics_status"

printf '%s\n' "==> Step 8 forced recovery failure persistence"
python3 scripts/test_recovery_failed_persistence.py

printf '%s\n' "==> Full Playwright suite"
(
  cd e2e
  E2E_BASE_URL="$E2E_BASE_URL" \
  E2E_EXPECTED_VERSION="$RELEASE_VERSION" \
  E2E_EXPECTED_COMMIT="$VCS_REF" \
  E2E_ADMIN_USERNAME="$E2E_ADMIN_USERNAME" \
  E2E_ADMIN_PASSWORD="$E2E_ADMIN_PASSWORD" \
  npx playwright test --workers=1
)

printf '%s\n' "==> Live backup / restore drill"
CPIM_BACKUP_DIR="$EVIDENCE_DIR/backups" ./scripts/restore_drill.sh
if [[ -d "$EVIDENCE_DIR/backups" ]] && find "$EVIDENCE_DIR/backups" -type f -print -quit | grep -q .; then
  fail "restore drill left backup payloads behind"
fi

printf '%s\n' "==> Security release evidence bundle"
python3 scripts/build_release_bundle.py \
  --dependency-reports "$SECURITY_REPORT_DIR" \
  --container-artifacts "$SECURITY_ARTIFACT_DIR" \
  --output "$SECURITY_BUNDLE_DIR" \
  --commit "$VCS_REF" \
  --build-time "$BUILD_TIME"
python3 scripts/verify_release_bundle.py "$SECURITY_BUNDLE_DIR"

printf '%s\n' "==> Final clean-room evidence"
e2e_count="$(grep -R -E '^\s*test\(' e2e/tests --include='*.spec.ts' | wc -l | tr -d ' ')"
[[ "$e2e_count" == "25" ]] || fail "expected 25 Playwright tests, found $e2e_count"
python3 - "$EVIDENCE_DIR/FINAL-CLEAN-ROOM-RESULT.json" <<'PY'
import json, os, sys
from pathlib import Path
out = Path(sys.argv[1])
data = {
  "schemaVersion": 1,
  "candidateVersion": os.environ["RC_VERSION"],
  "targetRelease": os.environ["RELEASE_VERSION"],
  "gitCommit": os.environ["VCS_REF"],
  "buildTime": os.environ["BUILD_TIME"],
  "status": "PASS",
  "migrationCount": 41,
  "migrationLatest": 41,
  "migration0042Created": False,
  "playwrightTests": 25,
  "productionTopology": {
    "databaseHostPublished": False,
    "backendHostPublished": False,
    "frontendOnlyPublic": True,
  },
  "gates": {
    "sourceChecksums": "PASS",
    "runtimeAndLocks": "PASS",
    "backendRaceVetTests": "PASS",
    "allStaticGuards": "PASS",
    "frontendTypecheckBuild": "PASS",
    "dependencySecurity": "PASS",
    "containerSecurityAndSBOM": "PASS",
    "emptyDatabaseMigration0001Through0041": "PASS",
    "versionBuildMetadata": "PASS",
    "observabilityBoundary": "PASS",
    "recoveryFailedRunPersistence": "PASS",
    "playwright": "PASS",
    "backupRestoreDrill": "PASS",
    "securityReleaseBundleVerification": "PASS",
  },
}
out.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
PY

printf '%s\n' "==> Build verified Release Candidate bundle"
python3 scripts/build_release_candidate_bundle.py \
  --clean-room-result "$EVIDENCE_DIR/FINAL-CLEAN-ROOM-RESULT.json" \
  --security-bundle "$SECURITY_BUNDLE_DIR" \
  --output "$RC_BUNDLE_DIR" \
  --commit "$VCS_REF" \
  --build-time "$BUILD_TIME" \
  --candidate "$RC_VERSION"
python3 scripts/verify_release_candidate_bundle.py "$RC_BUNDLE_DIR"

printf '%s\n' "FINAL CLEAN-ROOM GATE: PASS"
printf '  candidate=%s target=%s commit=%s migrations=41/0041 e2e=25\n' "$RC_VERSION" "$RELEASE_VERSION" "$VCS_REF"
