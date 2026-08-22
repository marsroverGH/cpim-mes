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
