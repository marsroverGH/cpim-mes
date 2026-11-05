# CPIM-MES v0.41.1 Step 8 — 0041 FAILED Run Persistence

Baseline: Step 7 User Administration complete bundle

## Scope

Implemented Production Hardening Step 8 without changing the 0041 database schema, Planning semantics, or operational side-effect boundary.

The original 0041 service inserted `RUNNING`, all result evidence, and `SUCCEEDED` inside one long transaction. If the simulation or evidence persistence failed before commit, the `RUNNING` row could be rolled back and disappear. Step 8 makes the run audit lifecycle durable.

## Transaction lifecycle

```text
Tx A
  validate actor / scenario / actions
  capture current Control Tower baseline
  calculate baseline_hash / request_hash
  reuse canonical SUCCEEDED evidence when already present
  INSERT recovery_scenario_runs(status=RUNNING)
  COMMIT

Pure simulation engine
  no DB handle

Tx B
  lock scenario
  revalidate DRAFT/SIMULATED lifecycle
  reload actions and recompute request_hash
  verify run is still RUNNING
  insert case/action/summary evidence
  RUNNING -> SUCCEEDED
  DRAFT -> SIMULATED when necessary
  COMMIT

Tx C on any post-Tx-A failure
  use context detached from HTTP cancellation
  lock recovery_scenario_runs row
  RUNNING -> FAILED
  result_hash = NULL
  completed_at = now()
  failure_reason = bounded error text
  COMMIT
```

The rollback defer executes before Tx C, so partial Tx B result rows cannot remain.

## Concurrency / stale-definition protection

Splitting the previous long transaction creates a possible interval in which a DRAFT scenario could be edited or its lifecycle changed. Step 8 therefore re-locks the scenario in Tx B, reloads all actions, recalculates `request_hash`, and compares it with the immutable run request created in Tx A.

If the lifecycle or actions changed, the run becomes FAILED instead of recording stale SUCCEEDED evidence.

The existing successful-request unique index remains unchanged:

```sql
UNIQUE (scenario_id, request_hash)
WHERE status = 'SUCCEEDED'
```

Therefore FAILED requests remain retryable while successful identical requests remain canonical/reusable.

## Failure finalization details

`markRecoveryRunFailed`:

- starts its own transaction
- uses `context.WithoutCancel` plus a 10-second timeout
- locks the run with `FOR UPDATE`
- only transitions `RUNNING -> FAILED`
- preserves already `FAILED` rows idempotently
- never rewrites an already `SUCCEEDED` row after an ambiguous commit result
- joins a Tx C persistence error with the original simulation error instead of hiding either failure
- limits stored `failure_reason` to 2000 Unicode code points

A process/host crash while a RUNNING row exists is intentionally not auto-classified as FAILED by this step; stale-run crash reconciliation remains a separate operations concern.

## Forced-failure integration gate

Added `scripts/test_recovery_failed_persistence.py`. It does not use a production failpoint. Instead, CI temporarily creates a PostgreSQL trigger that raises an exception before `recovery_scenario_summaries` insert, forcing a real Tx B database failure through the public API.

The gate verifies:

- forced failure returns non-success
- RUNNING remains as durable FAILED evidence
- `failure_reason` is non-empty
- `completed_at` is populated
- `result_hash` remains NULL
- case/action/summary child rows from the failed Tx B are rolled back
- FAILED run cannot be published
- the identical failed request retries successfully
- a later identical successful request reuses the canonical SUCCEEDED run
- Purchase Order, Work Order, WO operation, Work Center, Sales Order, and Inventory operational state hashes are unchanged

The temporary trigger/function are removed in a `finally` cleanup path.

## Files added

- `backend/scripts/check_recovery_failed_persistence.py`
- `scripts/test_recovery_failed_persistence.py`
- `STEP8-RECOVERY-FAILED-PERSISTENCE-REPORT.md`

## Files materially updated

- `backend/internal/service/recovery_planning.go`
- `backend/internal/service/recovery_planning_test.go`
- `docs/0041-scenario-based-recovery-planning.md`
- `.github/workflows/ci.yml`
- `Makefile`
- `README.md`
- `RELEASE-METADATA.txt`

## Database / migration state

No migration is added or modified.

```text
0001-0041
count = 41
latest = 0041
0042 = absent
```

The existing 0041 fields and constraints are reused:

- `RUNNING / SUCCEEDED / FAILED`
- `failure_reason`
- `completed_at`
- SUCCEEDED-only partial unique request index

## Validation completed in this environment

- Step 8 Recovery FAILED Persistence Static Guard: 57/57 PASS
- Step 7 User Administration Guard: 70/70 PASS
- Step 6 Login Rate Limit/Auth Audit Guard: 57/57 PASS
- Step 5 Cookie Auth/CORS/CSRF Guard: 47/47 PASS
- Step 4 Production Frontend Guard: 51/51 PASS
- Step 3 Demo Credentials Guard: 41/41 PASS
- Step 2 Production Config Guard: 41/41 PASS
- 0039 Static Guard: 106 checks PASS
- 0040 Static Guard: 95/95 PASS
- 0041 Static Guard: 98/98 PASS
- All current `backend/scripts/check_*.py`: PASS
- Runtime version policy: PASS
- Dependency lock check: PASS
- CI/dependency policy: PASS
- Python integration/static scripts compile: PASS
- Changed Go files: `gofmt` parse/format PASS

Step 7 baseline comparison confirms no change to:

```text
backend/migrations
backend/internal/domain
frontend/src
e2e/tests
```

Only the 0041 Recovery Planning service transaction lifecycle and its tests/gates/docs are changed within business code.

## Environment limitation

This execution environment has Go 1.23.2 while the project pins Go 1.26.x, cannot download the Go 1.26 toolchain/modules, and has no Docker daemon. Therefore the following runtime gates must execute on the development Mac or GitHub Actions:

```text
go mod verify
go vet ./...
go test -race ./...
frontend npm ci
frontend npm run typecheck
frontend npm run build
Docker production build
Step 8 forced-failure integration gate
Playwright 23/23
```

CI now runs the Step 8 static guard and the forced database-failure integration gate before the existing Playwright suite.
