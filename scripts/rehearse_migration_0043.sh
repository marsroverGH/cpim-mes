#!/usr/bin/env bash
# Rehearse migration 0043 (Quantity Column Scale) on a RESTORED COPY of the
# production database. Never point this at production itself.
#
#   preflight (default): changes nothing (temporary tables only, rolled back).
#     Reports row counts and sizes of the tables
#     0043 rewrites, stored float artefacts, and every condition that would make
#     0043 abort (values too large, ledger already out of balance, views or
#     triggers it cannot recreate).
#   --dry-run: also executes 0043 inside a transaction, times it, checks the
#     result and ROLLS BACK. It takes ACCESS EXCLUSIVE locks on 16 tables while
#     it runs, so it refuses to run while other sessions use the database.
#
# psql is taken from $PSQL (default: psql) and the connection from libpq
# environment variables or --database-url. With the Compose stack, for example:
#   PSQL="docker compose --env-file .env.rehearsal -f docker-compose.yml -f docker-compose.prod.yml exec -T db psql -U cpim -d cpim_rehearsal" \
#     ./scripts/rehearse_migration_0043.sh --dry-run
# See docs/MIGRATION_0043_REHEARSAL.md.
#
# Exit status: 0 no blocker found, 1 0043 would abort (or dry run failed), 2 usage/connection error.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIGRATION="$ROOT/backend/migrations/0043_quantity_column_scale.sql"
ARTEFACT_CHECK="$ROOT/scripts/sql/check_quantity_float_artefacts.sql"
DRY_RUN=0
ALLOW_OTHER_SESSIONS=0
DATABASE_URL_ARG=""
MIGRATION_TIMEOUT_SECONDS="${MIGRATION_TIMEOUT_SECONDS:-600}"

usage() {
  awk 'NR > 1 && !/^#/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
  echo
  echo "Usage: $0 [--database-url URL] [--dry-run] [--allow-other-sessions]"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --allow-other-sessions) ALLOW_OTHER_SESSIONS=1 ;;
    --database-url) shift; DATABASE_URL_ARG="${1:-}"; [ -n "$DATABASE_URL_ARG" ] || { usage >&2; exit 2; } ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

read -r -a PSQL_CMD <<<"${PSQL:-psql}"
PSQL_ARGS=(-X -q -v ON_ERROR_STOP=1 -P pager=off)
if [ -n "$DATABASE_URL_ARG" ]; then
  PSQL_ARGS+=(-d "$DATABASE_URL_ARG")
fi
run_psql() { "${PSQL_CMD[@]}" "${PSQL_ARGS[@]}" "$@"; }

[ -f "$MIGRATION" ] || { echo "missing $MIGRATION" >&2; exit 2; }
[ -f "$ARTEFACT_CHECK" ] || { echo "missing $ARTEFACT_CHECK" >&2; exit 2; }

# The column list is taken from 0043 itself so the rehearsal checks exactly
# what the migration will change.
COLUMN_LIST_SQL="$(sed -n '/^CREATE TEMP TABLE quantity_scale_0043_columns/,/;$/{p;}' "$MIGRATION" | sed -n '1p')
$(sed -n '/^INSERT INTO quantity_scale_0043_columns/,/;$/p' "$MIGRATION")"
LISTED=$(printf '%s\n' "$COLUMN_LIST_SQL" | grep -c "^    ('" || true)
if [ "$LISTED" -ne 25 ]; then
  echo "could not read the 25 column list from $MIGRATION (got $LISTED)" >&2
  exit 2
fi

if ! run_psql -tAc 'SELECT 1' >/dev/null; then
  echo "cannot connect with: ${PSQL_CMD[*]} ${DATABASE_URL_ARG:+-d <url>}" >&2
  exit 2
fi

migration_sha=$(shasum -a 256 "$MIGRATION" | awk '{print $1}')
echo "== 0043 rehearsal $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "migration file sha256: $migration_sha"

state=$(run_psql -tAc "SELECT current_database() || ' / ' || version() FROM (SELECT 1) s")
echo "database: $state"
latest=$(run_psql -tAc "SELECT coalesce(max(version),0) FROM schema_migrations" 2>/dev/null || echo "none")
echo "schema_migrations latest: $latest"
case "$latest" in
  42) ;;
  43) echo "0043 is already applied; nothing to rehearse."; exit 0 ;;
  *) echo "BLOCKER: the database must be at migration 42 (v0.42.x) to rehearse 0043" >&2; exit 1 ;;
esac

echo
echo "== Preflight (no changes; rolled back)"
# Only temporary tables are written; the transaction is rolled back.
if ! preflight_out=$(run_psql 2>&1 <<SQL
BEGIN;
\pset footer off
$COLUMN_LIST_SQL

\echo
\echo '-- tables 0043 rewrites (exact row counts, total size incl. indexes)'
SELECT t.table_name, (xpath('/row/n/text()', query_to_xml(format('SELECT count(*) AS n FROM public.%I', t.table_name), false, true, '')))[1]::text::bigint AS rows,
       pg_size_pretty(pg_total_relation_size(format('public.%I', t.table_name)::regclass)) AS total_size
  FROM (SELECT DISTINCT table_name FROM quantity_scale_0043_columns) t
 ORDER BY pg_total_relation_size(format('public.%I', t.table_name)::regclass) DESC;
SELECT pg_size_pretty(sum(pg_total_relation_size(format('public.%I', table_name)::regclass))) AS total_rewritten
  FROM (SELECT DISTINCT table_name FROM quantity_scale_0043_columns) t;

\echo
\echo '-- columns: current type, values over 9 decimals, values too large for numeric(20,9)'
CREATE TEMP TABLE r43_cols ON COMMIT DROP AS
SELECT q.table_name, q.column_name, c.data_type, c.numeric_scale,
       CASE WHEN c.data_type = 'numeric' THEN (xpath('/row/n/text()', query_to_xml(format(
            'SELECT count(*) FILTER (WHERE scale(%1\$I) > 9) AS n FROM public.%2\$I', q.column_name, q.table_name), false, true, '')))[1]::text::bigint END AS over_9,
       CASE WHEN c.data_type = 'numeric' THEN (xpath('/row/n/text()', query_to_xml(format(
            'SELECT count(*) FILTER (WHERE abs(%1\$I) >= 99999999999.9999999995) AS n FROM public.%2\$I', q.column_name, q.table_name), false, true, '')))[1]::text::bigint END AS too_large
  FROM quantity_scale_0043_columns q
  LEFT JOIN information_schema.columns c
    ON c.table_schema = 'public' AND c.table_name = q.table_name AND c.column_name = q.column_name;
SELECT table_name, column_name, coalesce(data_type, 'MISSING') AS type, numeric_scale AS scale, over_9, too_large
  FROM r43_cols ORDER BY table_name, column_name;

\echo
\echo '-- views 0043 drops and recreates (v0.42.x has 7)'
CREATE TEMP TABLE r43_views ON COMMIT DROP AS
SELECT DISTINCT vc.oid, vc.relname AS view_name
  FROM quantity_scale_0043_columns q
  JOIN pg_class tc ON tc.relname = q.table_name AND tc.relnamespace = 'public'::regnamespace
  JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = q.column_name
  JOIN pg_depend d ON d.refobjid = tc.oid AND d.refobjsubid = a.attnum AND d.classid = 'pg_rewrite'::regclass
  JOIN pg_rewrite r ON r.oid = d.objid
  JOIN pg_class vc ON vc.oid = r.ev_class AND vc.oid <> tc.oid AND vc.relkind = 'v';
SELECT view_name FROM r43_views ORDER BY 1;
CREATE TEMP TABLE r43_nested ON COMMIT DROP AS
SELECT DISTINCT dep.relname AS view_name, dep.relkind
  FROM pg_depend d
  JOIN pg_rewrite r ON r.oid = d.objid
  JOIN pg_class dep ON dep.oid = r.ev_class
 WHERE d.classid = 'pg_rewrite'::regclass
   AND d.refobjid IN (SELECT oid FROM r43_views)
   AND dep.oid NOT IN (SELECT oid FROM r43_views)
   AND dep.oid <> d.refobjid;
CREATE TEMP TABLE r43_other_deps ON COMMIT DROP AS
SELECT DISTINCT pg_describe_object(d.classid, d.objid, d.objsubid) AS object, q.table_name || '.' || q.column_name AS column_ref
  FROM quantity_scale_0043_columns q
  JOIN pg_class tc ON tc.relname = q.table_name AND tc.relnamespace = 'public'::regnamespace
  JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = q.column_name
  JOIN pg_depend d ON d.refobjid = tc.oid AND d.refobjsubid = a.attnum
 WHERE d.classid = 'pg_rewrite'::regclass
   AND EXISTS (SELECT 1 FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class
                WHERE r.oid = d.objid AND c.relkind = 'm');

\echo
\echo '-- column triggers 0043 drops and recreates (v0.42.x has 1)'
CREATE TEMP TABLE r43_triggers ON COMMIT DROP AS
SELECT DISTINCT t.tgname AS trigger_name, tc.relname AS table_name, t.tgenabled AS enabled
  FROM quantity_scale_0043_columns q
  JOIN pg_class tc ON tc.relname = q.table_name AND tc.relnamespace = 'public'::regnamespace
  JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = q.column_name
  JOIN pg_depend d ON d.refobjid = tc.oid AND d.refobjsubid = a.attnum AND d.classid = 'pg_trigger'::regclass
  JOIN pg_trigger t ON t.oid = d.objid AND NOT t.tgisinternal;
SELECT table_name, trigger_name, enabled FROM r43_triggers ORDER BY 1, 2;

\echo
\echo '-- ledger before 0043 (0043 aborts if these do not hold after rounding)'
CREATE TEMP TABLE r43_ledger ON COMMIT DROP AS
SELECT (SELECT count(*) FROM v_inventory_lot_reconciliation WHERE abs(difference) > 0.000001) AS unreconciled_items,
       (SELECT count(*) FROM (SELECT lot_id FROM lot_movements GROUP BY lot_id HAVING sum(quantity) < -0.000001) s) AS negative_lots,
       (SELECT count(*) FROM v_inventory_lot_reconciliation
         WHERE abs(difference) <= 0.000001 AND abs(difference) > 0.0000005) AS near_tolerance_items;
SELECT * FROM r43_ledger;

\echo
\echo '-- stored float artefacts (scripts/sql/check_quantity_float_artefacts.sql; all unconstrained numeric columns)'
SELECT * FROM (
$(sed -e 's/;[[:space:]]*$//' "$ARTEFACT_CHECK")
) artefacts;

\echo
\echo '-- other sessions on this database'
SELECT count(*) AS other_sessions FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend';

\echo
\echo '-- blockers'
CREATE TEMP TABLE r43_blockers (reason text) ON COMMIT DROP;
INSERT INTO r43_blockers
SELECT format('%s.%s is not an unconstrained numeric (type %s, scale %s)', table_name, column_name, coalesce(data_type, 'missing'), numeric_scale)
  FROM r43_cols WHERE data_type IS DISTINCT FROM 'numeric' OR numeric_scale IS NOT NULL;
INSERT INTO r43_blockers
SELECT format('%s.%s has %s value(s) too large for numeric(20,9)', table_name, column_name, too_large) FROM r43_cols WHERE too_large > 0;
INSERT INTO r43_blockers
SELECT format('view %s depends on a view 0043 recreates', view_name) FROM r43_nested;
INSERT INTO r43_blockers
SELECT format('%s depends on %s (materialized view; 0043 does not recreate it)', object, column_ref) FROM r43_other_deps;
INSERT INTO r43_blockers
SELECT format('trigger %s on %s has enabled state %s (0043 supports only enabled/disabled)', trigger_name, table_name, enabled)
  FROM r43_triggers WHERE enabled NOT IN ('O', 'D');
INSERT INTO r43_blockers
SELECT format('%s item(s) already differ between inventory and lot ledger', unreconciled_items) FROM r43_ledger WHERE unreconciled_items > 0;
INSERT INTO r43_blockers
SELECT format('%s lot(s) already have a negative balance', negative_lots) FROM r43_ledger WHERE negative_lots > 0;
SELECT coalesce(string_agg(reason, E'\n' ORDER BY reason), 'none') AS blockers FROM r43_blockers;
SELECT count(*) AS blocker_count FROM r43_blockers \gset
\echo BLOCKER_COUNT=:blocker_count
ROLLBACK;
SQL
); then
  printf '%s\n' "$preflight_out"
  echo "RESULT: preflight could not run (see the error above)." >&2
  exit 2
fi
printf '%s\n' "$preflight_out" | grep -v '^BLOCKER_COUNT='
blockers=$(printf '%s\n' "$preflight_out" | sed -n 's/^BLOCKER_COUNT=//p')
if [ "$blockers" != "0" ]; then
  echo
  echo "RESULT: 0043 would abort ($blockers blocker(s) above). Fix the data on a copy and rehearse again; see docs/MIGRATION_0043_REHEARSAL.md."
  exit 1
fi

if [ "$DRY_RUN" -ne 1 ]; then
  echo
  echo "RESULT: preflight PASS (no blocker). Run again with --dry-run on the restored copy to time 0043."
  exit 0
fi

others=$(run_psql -tAc "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'")
if [ "$others" != "0" ] && [ "$ALLOW_OTHER_SESSIONS" -ne 1 ]; then
  echo
  echo "REFUSED: $others other session(s) are connected to this database. The dry run locks 16 tables;" >&2
  echo "run it only on a restored copy with the backend stopped (or pass --allow-other-sessions)." >&2
  exit 2
fi

echo
echo "== Dry run (0043 executed inside a transaction, then ROLLBACK)"
start=$(date +%s)
set +e
dry_out=$(run_psql 2>&1 <<SQL
\timing on
SET lock_timeout = '10s';
BEGIN;
$(cat "$MIGRATION")
\timing off
\pset footer off
\echo '-- after 0043 (inside the transaction)'
SELECT count(*) AS numeric_20_9_columns FROM information_schema.columns c
  JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
 WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
   AND c.data_type = 'numeric' AND c.numeric_precision = 20 AND c.numeric_scale = 9;
SELECT count(*) AS unreconciled_items FROM v_inventory_lot_reconciliation WHERE abs(difference) > 0.000001;
\echo '-- stored float artefacts remaining (columns 0043 does not change may still be listed)'
SELECT * FROM (
$(sed -e 's/;[[:space:]]*$//' "$ARTEFACT_CHECK")
) artefacts;
ROLLBACK;
\echo DRY_RUN_ROLLED_BACK
SQL
)
status=$?
set -e
end=$(date +%s)
printf '%s\n' "$dry_out" | grep -v '^DRY_RUN_ROLLED_BACK$'
elapsed=$((end - start))
if [ $status -ne 0 ] || ! printf '%s\n' "$dry_out" | grep -q '^DRY_RUN_ROLLED_BACK$'; then
  echo
  echo "RESULT: dry run FAILED after ${elapsed}s (psql exit $status). Nothing was committed."
  exit 1
fi
echo
echo "dry run wall time: ${elapsed}s (MIGRATION_TIMEOUT budget: ${MIGRATION_TIMEOUT_SECONDS}s)"
if [ "$elapsed" -ge $((MIGRATION_TIMEOUT_SECONDS / 2)) ]; then
  echo "WARNING: more than half of MIGRATION_TIMEOUT. Raise MIGRATION_TIMEOUT for the upgrade (production hardware and cache state differ)."
fi
echo "RESULT: dry run PASS; rolled back, the database is unchanged."
