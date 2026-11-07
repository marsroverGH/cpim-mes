#!/usr/bin/env bash
# Reproduces the CI "Integration / Playwright" job locally (isolated project + port).
set -u
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME=cpim-mes-measure
export CPIM_HTTP_PORT=18080
export POSTGRES_PASSWORD=ci-db-password
export DATABASE_URL='postgres://cpim:ci-db-password@db:5432/cpim?sslmode=disable'
export JWT_SECRET=ci-only-secret-not-for-production
export VCS_REF="$(git rev-parse HEAD)"
export BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
export BOOTSTRAP_ADMIN_USERNAME="local-admin-$(date +%s)"
F="-f docker-compose.yml -f docker-compose.prod.yml -f docker-compose.bootstrap.yml"
LOG=.measure
mkdir -p "$LOG"

mkdir -p .secrets
openssl rand -base64 36 | tr -d '\n' > .secrets/bootstrap-admin-password
chmod 600 .secrets/bootstrap-admin-password
export E2E_ADMIN_USERNAME="$BOOTSTRAP_ADMIN_USERNAME"
export E2E_ADMIN_PASSWORD="$(cat .secrets/bootstrap-admin-password)"
export E2E_BASE_URL="http://localhost:${CPIM_HTTP_PORT}"
export E2E_EXPECTED_VERSION=v0.43.0
export E2E_EXPECTED_COMMIT="$VCS_REF"

step() { echo; echo "===== $1 ($(date +%T))"; }

step "docker compose build+up"
start=$(date +%s)
docker compose $F up -d --build > $LOG/docker_build.log 2>&1
echo "EXIT $? (build+up $(( $(date +%s)-start ))s)"

step "wait for health"
ok=0
for i in $(seq 1 90); do
  if curl -fsS $E2E_BASE_URL/healthz >/dev/null 2>&1 && curl -fsS $E2E_BASE_URL/version >/dev/null 2>&1; then ok=1; break; fi
  sleep 2
done
echo "healthy=$ok"
curl -sS $E2E_BASE_URL/version; echo
docker compose $F logs --no-color backend > $LOG/backend_startup.log 2>&1
grep -iE "migrat" $LOG/backend_startup.log | tail -5

if [ "$ok" = 1 ]; then
  step "schema_migrations"
  docker compose $F exec -T db psql -U cpim -d cpim -Atc "select count(*), max(version) from schema_migrations" 2>&1

  step "observability gate"
  docker compose $F exec -T backend wget -qO- http://127.0.0.1:8080/metrics > $LOG/metrics.txt 2>&1
  for m in http_requests_total db_open_connections mrp_runs_total recovery_simulation_failed_total; do
    grep -q "^# HELP $m " $LOG/metrics.txt && echo "metric $m OK" || echo "metric $m MISSING"
  done
  echo "public /metrics status: $(curl -sS -o /dev/null -w '%{http_code}' $E2E_BASE_URL/metrics)"

  step "forced recovery failure persistence gate"
  python3 scripts/test_recovery_failed_persistence.py > $LOG/failed_persistence.log 2>&1
  echo "EXIT $?"; tail -5 $LOG/failed_persistence.log

  step "playwright"
  ( cd e2e && npm ci --no-audit --no-fund > ../$LOG/e2e_npm.log 2>&1 && npx playwright install chromium >> ../$LOG/e2e_npm.log 2>&1 \
    && npx playwright test --workers=1 --retries=0 --reporter=list > ../$LOG/playwright.log 2>&1 ); echo "EXIT $?"
  tail -40 $LOG/playwright.log

  step "restore drill"
  CPIM_BACKUP_DIR=/tmp/cpim-mes-measure-backups ./scripts/restore_drill.sh > $LOG/restore_drill.log 2>&1
  echo "EXIT $?"; tail -8 $LOG/restore_drill.log
fi

step "teardown"
docker compose $F logs --no-color > $LOG/compose_all.log 2>&1
docker compose $F down -v --remove-orphans > /dev/null 2>&1; echo "down EXIT $?"
rm -f .secrets/bootstrap-admin-password
