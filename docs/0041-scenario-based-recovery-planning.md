# 0041 Scenario-Based Recovery Planning / What-if Simulation

## Purpose

0041 extends the Production Control Tower with side-effect-free recovery planning.

Planners can create multiple hypothetical recovery scenarios, simulate their
effects against the current Control Tower baseline, compare business impact,
and publish an approved Recovery Plan as immutable evidence.

Simulation and Publish do not directly execute operational changes.

## Recovery Actions

Supported actions:

- EXPEDITE_PO
- ALTERNATE_WORK_CENTER
- ADD_OVERTIME_CAPACITY
- RESCHEDULE_WO
- RELEASE_WO

## Scenario Lifecycle

DRAFT -> SIMULATED -> PUBLISHED

ARCHIVED is also supported.

Scenario definitions and actions are mutable only while DRAFT.

## Canonical Simulation

Successful simulations record:

- baseline_hash
- request_hash
- result_hash

Repeated successful requests reuse canonical evidence.

The unique successful-request constraint applies only to SUCCEEDED runs,
so failed requests remain retryable.

## FAILED Run Persistence Hardening (v0.41.1 Step 8)

A simulation run is now persisted in three explicit transaction phases so an
application failure cannot erase the run audit row together with its results.

```text
Tx A
  validate actor / scenario / actions
  capture Control Tower baseline
  calculate baseline_hash / request_hash
  reuse existing SUCCEEDED evidence when present
  INSERT recovery_scenario_runs status=RUNNING
  COMMIT

Pure simulation engine
  no DB handle

Tx B
  re-lock scenario
  re-check scenario lifecycle
  recompute request_hash from the current action definition
  verify the run is still RUNNING
  INSERT case/action/summary evidence
  RUNNING -> SUCCEEDED
  DRAFT -> SIMULATED when required
  COMMIT

Tx C (only after any post-Tx-A failure)
  detach from HTTP request cancellation
  lock the run
  RUNNING -> FAILED
  result_hash = NULL
  failure_reason = bounded error text
  completed_at = now()
  COMMIT
```

If Tx B fails, its result rows roll back atomically and Tx C leaves the original
RUNNING row as durable FAILED evidence. A FAILED run cannot be published.
FAILED requests remain retryable because the unique request index continues to
cover only SUCCEEDED rows. Once an identical request succeeds, subsequent
identical requests reuse that canonical SUCCEEDED run.

Tx B also revalidates the scenario/action definition after the pure simulation.
If a DRAFT action was edited or the scenario lifecycle changed between Tx A and
Tx B, the in-flight run becomes FAILED rather than persisting stale success
evidence. This replaces the consistency protection that the previous single
long transaction obtained by holding locks during the entire simulation.

The failure finalizer uses a short context detached from request cancellation,
so a client disconnect after Tx A does not by itself erase the FAILED audit
transition. A process/host crash while a run is RUNNING is a separate crash
reconciliation concern and is not silently treated as FAILED by this change.

### Step 8 verification

Static guard:

```bash
python3 backend/scripts/check_recovery_failed_persistence.py
```

With the production Compose topology already running, the integration gate
installs a temporary PostgreSQL trigger that intentionally fails Tx B while
inserting the recovery summary. No test-only failpoint exists in product code.

```bash
python3 scripts/test_recovery_failed_persistence.py
```

The integration gate verifies:

- forced failure leaves a FAILED run
- failure_reason and completed_at are present
- result_hash remains NULL
- Tx B child evidence is rolled back
- FAILED cannot publish
- the same failed request can retry and SUCCEED
- the next identical successful request reuses the canonical run
- operational Purchase Order / Work Order / Work Center / Sales Order /
  Inventory state remains unchanged

## Scenario Comparison

Only scenarios sharing the same baseline_hash are ranked together.

Ranking priority:

1. P1 Reduction
2. Recovered Revenue
3. Impact Days Recovered
4. Net Value
5. Recovery Score
6. Lower Estimated Action Cost

## Recovery Score

Recovery Score is bounded from 0 through 100.

Weighting:

- P1 reduction: 35%
- Revenue recovered: 30%
- Impact days recovered: 20%
- Open-case reduction: 10%
- Cost efficiency: 5%

Recovery Score is decision support only.

## Immutable Evidence

Immutable evidence is stored in:

- recovery_scenario_runs
- recovery_scenario_case_results
- recovery_scenario_action_results
- recovery_scenario_summaries
- recovery_scenario_publications

## Operational Safety Boundary

Recovery Planning does not directly mutate:

- Purchase Orders
- Purchase Order Lines
- Work Orders
- Work Order Operations
- Work Centers
- Sales Orders
- Sales Order Lines
- Inventory Lots
- Inventory Transactions

Publish records approved Recovery Plan evidence only.

## RBAC

Permissions:

- planning.recovery_scenario.manage
- planning.recovery_scenario.simulate
- planning.recovery_scenario.publish

Planner and Admin can manage Recovery Planning.
Operator cannot mutate, simulate, or publish scenarios.

## API

Main paths:

- /api/recovery-scenarios
- /api/recovery-scenarios/{id}
- /api/recovery-scenarios/{id}/actions
- /api/recovery-scenarios/{id}/simulate
- /api/recovery-scenario-comparison
- /api/recovery-scenarios/{id}/publish

## UI

Recovery Planning UI:

/recovery-planning

The UI supports scenario definition, What-if simulation, comparison,
Recovery Score, P1 Reduction, Revenue Recovered, Impact Days Recovered,
Estimated Action Cost, Net Value, and publication.

## Verification

Static Guard:

python3 backend/scripts/check_recovery_planning.py

Dedicated E2E:

E2E_BASE_URL=http://localhost:55173 npx playwright test tests/recovery-planning.spec.ts --workers=1

The dedicated E2E verifies canonical reuse, comparison, publication,
PUBLISHED immutability, UI operation, and operational side-effect boundaries.

## Release Acceptance

Release requires:

- Empty DB migration 0001 through 0041
- Migration ledger count = 41
- go test ./...
- go vet ./...
- Frontend typecheck
- Frontend production build
- Recovery Planning Static Guard
- Dedicated Recovery Planning E2E
- Full Playwright E2E
- git diff --check
