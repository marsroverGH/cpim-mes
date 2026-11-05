#!/usr/bin/env python3
"""Integration gate for Step 8 recovery FAILED-run durability.

Requires the production Docker Compose topology to already be running and the
one-time E2E administrator credentials to be present in the environment.
The test injects a temporary PostgreSQL trigger that fails summary persistence
inside Tx B. Product code contains no test-only failpoint.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
BASE_URL = os.environ.get("E2E_BASE_URL", "http://localhost:8080").rstrip("/")
USERNAME = os.environ.get("E2E_ADMIN_USERNAME", "")
PASSWORD = os.environ.get("E2E_ADMIN_PASSWORD", "")
POSTGRES_USER = os.environ.get("POSTGRES_USER", "cpim")
POSTGRES_DB = os.environ.get("POSTGRES_DB", "cpim")

COMPOSE = [
    "docker", "compose",
    "-f", str(ROOT / "docker-compose.yml"),
    "-f", str(ROOT / "docker-compose.prod.yml"),
]


def fail(message: str) -> None:
    raise AssertionError(message)


def api(method: str, path: str, payload=None, token: str | None = None):
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"

    request = urllib.request.Request(
        f"{BASE_URL}{path}", data=data, headers=headers, method=method
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read().decode("utf-8")
            return response.status, json.loads(body) if body else None
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8")
        try:
            parsed = json.loads(body) if body else None
        except json.JSONDecodeError:
            parsed = body
        return exc.code, parsed


def psql(sql: str, *, tuples: bool = False) -> str:
    command = COMPOSE + [
        "exec", "-T", "db", "psql",
        "-v", "ON_ERROR_STOP=1",
        "-U", POSTGRES_USER,
        "-d", POSTGRES_DB,
    ]
    if tuples:
        command += ["-A", "-t", "-q"]
    command += ["-c", sql]
    result = subprocess.run(
        command,
        cwd=ROOT,
        env=os.environ.copy(),
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    return result.stdout.strip()


OPERATIONAL_TABLES = [
    "inventory_txns",
    "operation_logs",
    "purchase_orders",
    "purchase_receipts",
    "sales_order_lines",
    "sales_orders",
    "wo_operations",
    "work_centers",
    "work_order_completions",
    "work_orders",
]


def operational_snapshot() -> str:
    parts = []
    for table in OPERATIONAL_TABLES:
        sql = f"""
SELECT md5(
  COALESCE(
    (
      SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text
      FROM {table} t
    ),
    '[]'
  )
);
"""
        parts.append(f"{table}:{psql(sql, tuples=True)}")
    return "\n".join(parts)


def install_failure_trigger() -> None:
    psql("""
DROP TRIGGER IF EXISTS e2e_force_recovery_summary_failure
ON recovery_scenario_summaries;
DROP FUNCTION IF EXISTS e2e_force_recovery_summary_failure();
CREATE FUNCTION e2e_force_recovery_summary_failure()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'E2E forced recovery persistence failure';
END;
$$;
CREATE TRIGGER e2e_force_recovery_summary_failure
BEFORE INSERT ON recovery_scenario_summaries
FOR EACH ROW
EXECUTE FUNCTION e2e_force_recovery_summary_failure();
""")


def remove_failure_trigger() -> None:
    psql("""
DROP TRIGGER IF EXISTS e2e_force_recovery_summary_failure
ON recovery_scenario_summaries;
DROP FUNCTION IF EXISTS e2e_force_recovery_summary_failure();
""")


def latest_run(scenario_id: str) -> dict:
    raw = psql(f"""
SELECT row_to_json(x)::text
FROM (
  SELECT
    id::text AS id,
    status,
    request_hash,
    result_hash,
    completed_at IS NOT NULL AS completed,
    failure_reason
  FROM recovery_scenario_runs
  WHERE scenario_id='{scenario_id}'::uuid
  ORDER BY started_at DESC,id DESC
  LIMIT 1
) x;
""", tuples=True)
    if not raw:
        fail("no recovery_scenario_run persisted")
    return json.loads(raw)


def main() -> int:
    if not USERNAME or not PASSWORD:
        print("ERROR: E2E_ADMIN_USERNAME/E2E_ADMIN_PASSWORD are required", file=sys.stderr)
        return 2

    status, auth = api(
        "POST", "/api/auth/token", {"username": USERNAME, "password": PASSWORD}
    )
    if status != 200 or not isinstance(auth, dict) or not auth.get("token"):
        fail(f"admin token failed: HTTP {status} {auth!r}")
    token = auth["token"]

    before = operational_snapshot()

    status, scenario = api(
        "POST",
        "/api/recovery-scenarios",
        {"name": "E2E Step8 FAILED persistence", "description": "forced Tx B failure"},
        token,
    )
    if status != 201:
        fail(f"scenario create failed: HTTP {status} {scenario!r}")
    scenario_id = scenario["id"]

    status, action = api(
        "POST",
        f"/api/recovery-scenarios/{scenario_id}/actions",
        {
            "actionType": "RELEASE_WO",
            "targetType": "WORK_ORDER",
            "targetRef": "*",
            "parameters": {},
            "estimatedCost": 0,
            "note": "Step8 forced-failure integration gate",
        },
        token,
    )
    if status != 201:
        fail(f"action create failed: HTTP {status} {action!r}")

    try:
        install_failure_trigger()

        status, body = api(
            "POST",
            f"/api/recovery-scenarios/{scenario_id}/simulate",
            {"horizonDays": 90},
            token,
        )
        if 200 <= status < 300:
            fail(f"forced simulation unexpectedly succeeded: {body!r}")

        failed = latest_run(scenario_id)
        if failed["status"] != "FAILED":
            fail(f"forced run status = {failed['status']!r}; want FAILED")
        if not failed["completed"]:
            fail("FAILED run completed_at was not persisted")
        if failed["result_hash"] is not None:
            fail("FAILED run result_hash must remain NULL")
        if not str(failed["failure_reason"]).strip():
            fail("FAILED run failure_reason was not persisted")

        evidence = json.loads(psql(f"""
SELECT json_build_object(
  'cases', (SELECT count(*) FROM recovery_scenario_case_results WHERE run_id='{failed['id']}'::uuid),
  'actions', (SELECT count(*) FROM recovery_scenario_action_results WHERE run_id='{failed['id']}'::uuid),
  'summaries', (SELECT count(*) FROM recovery_scenario_summaries WHERE run_id='{failed['id']}'::uuid)
)::text;
""", tuples=True))
        if evidence != {"cases": 0, "actions": 0, "summaries": 0}:
            fail(f"failed Tx B leaked evidence rows: {evidence!r}")

        # FAILED evidence must never be publishable.
        publish_status, _ = api(
            "POST",
            f"/api/recovery-scenarios/{scenario_id}/publish",
            {"runId": failed["id"], "comment": "must fail"},
            token,
        )
        if 200 <= publish_status < 300:
            fail("FAILED run was publishable")

    finally:
        remove_failure_trigger()

    # The exact same failed request is retryable because only SUCCEEDED rows
    # participate in recovery_scenario_runs_success_request_uidx.
    status, success = api(
        "POST",
        f"/api/recovery-scenarios/{scenario_id}/simulate",
        {"horizonDays": 90},
        token,
    )
    if status != 201 or success.get("run", {}).get("status") != "SUCCEEDED":
        fail(f"retry did not succeed: HTTP {status} {success!r}")
    if success.get("reused") is not False:
        fail("first successful retry must create new immutable success evidence")

    if success["run"]["requestHash"] != failed["request_hash"]:
        fail("retry request_hash changed after forced failure")

    counts = json.loads(psql(f"""
SELECT json_build_object(
  'failed', count(*) FILTER (WHERE status='FAILED'),
  'succeeded', count(*) FILTER (WHERE status='SUCCEEDED')
)::text
FROM recovery_scenario_runs
WHERE scenario_id='{scenario_id}'::uuid
  AND request_hash='{failed['request_hash']}';
""", tuples=True))
    if int(counts["failed"]) < 1 or int(counts["succeeded"]) != 1:
        fail(f"unexpected retry evidence counts: {counts!r}")

    status, repeated = api(
        "POST",
        f"/api/recovery-scenarios/{scenario_id}/simulate",
        {"horizonDays": 90},
        token,
    )
    if status != 201 or repeated.get("reused") is not True:
        fail(f"successful identical retry was not reused: HTTP {status} {repeated!r}")
    if repeated["run"]["id"] != success["run"]["id"]:
        fail("canonical successful run ID was not reused")

    after = operational_snapshot()
    if after != before:
        print("--- operational snapshot before ---", file=sys.stderr)
        print(before, file=sys.stderr)
        print("--- operational snapshot after ---", file=sys.stderr)
        print(after, file=sys.stderr)
        fail("Recovery Simulation changed operational state")

    print("PASS: forced Tx B failure persists FAILED evidence")
    print("PASS: FAILED result_hash is NULL and evidence children rolled back")
    print("PASS: FAILED run cannot publish")
    print("PASS: failed identical request retries successfully")
    print("PASS: successful identical request reuses canonical SUCCEEDED run")
    print("PASS: operational state is unchanged")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"FAILED: {exc}", file=sys.stderr)
        raise
