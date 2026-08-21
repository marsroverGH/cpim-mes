# 0039 Real-Time Dispatching + Dynamic Rescheduling + Schedule Adherence

0039 turns the immutable Detailed Scheduling snapshot into a controlled execution schedule without rewriting historical planning or Shop Floor actuals.

## Execution model

A normal Detailed Scheduling run is persisted as usual and, after it reaches `COMPLETE`, becomes the single **Active Execution Schedule**. The active pointer is not inferred from "latest run"; it is explicitly stored in `detailed_schedule_execution_state` and every switch is recorded in immutable `detailed_schedule_activation_history`.

Dynamic rescheduling never edits an existing Detailed Schedule. It creates a new candidate run, compares it with the active run, records immutable change evidence, applies the active dispatch policy, and switches the active pointer only when the candidate passes the frozen-horizon guard.

## Time fences

The active policy is versioned and uses minute-based fences:

- `EXECUTED`: Shop Floor operation is already IN_PROGRESS, PAUSED or COMPLETED. Actual execution evidence remains authoritative and is never rewritten by rescheduling.
- `FROZEN`: future commitment inside `freeze_minutes`. Changing an existing frozen operation blocks automatic activation.
- `FIRM`: inside `firm_minutes` but outside the frozen zone. Changes may activate but are explicitly counted and surfaced to Pegging/Exception Management.
- `FLEXIBLE`: outside the firm zone and freely reschedulable within finite-capacity constraints.

A newly released operation that did not exist in the source schedule is not treated as a frozen-commitment violation merely because its new start falls inside the frozen window.

## Real-time Dispatch List

`GET /api/dispatch` projects the active execution run together with current `wo_operations` actual state. Ranking combines:

- execution urgency (`LATE_COMPLETE`, `IN_PROCESS`, `PAUSED`, `LATE_START`, `READY`, `QUEUED`),
- Frozen/Firm priority,
- Detailed Scheduling priority,
- due-date urgency,
- sequence-dependent setup-family match bonus.

The dispatch projection also reports planned/actual timestamps, start/completion variance, time fence, blocked reason and a deterministic dispatch score.

## Schedule Adherence

Adherence compares the active execution schedule with Shop Floor actuals. It records:

- planned start/end,
- actual start/end,
- start variance minutes,
- completion variance minutes,
- on-time start/completion flags,
- blocked/paused execution state,
- time-fence classification.

Future untouched operations are not yet adherence observations and therefore do not inflate the on-time denominator. An operation enters the start/completion KPI denominator after the actual occurs or the configured lateness threshold is missed.

Snapshots use Repeatable Read and a canonical SHA-256 hash. Snapshot rows are append-only evidence.

## Autonomous rescheduling signals

PostgreSQL queues auditable `schedule_reschedule_signals` from authoritative transaction evidence:

- Shop Floor START / STOP / COMPLETE / SCRAP,
- Breakdown / Unplanned Downtime / Maintenance revision,
- OEE Capacity Feedback activation,
- Quality status transition to HOLD.

The service bridge processes pending signals only after the primary transaction commits. If the autonomous reschedule fails, the production transaction is not rolled back and the pending signal remains available for retry.

Supported trigger vocabulary also includes material shortage and priority change for explicit/manual integrations.

## Dynamic reschedule lifecycle

A run starts as `EVALUATING` and ends as exactly one of:

- `ACTIVATED`: candidate has changes and no frozen conflict; active execution pointer switches atomically.
- `BLOCKED`: at least one existing frozen commitment would change.
- `NO_CHANGE`: candidate is equivalent for auditable operation commitments; active pointer is not churned.
- `FAILED`: evaluation/persistence/activation failed; `finished_at` is always recorded.
- `THROTTLED`: reserved DB vocabulary for explicitly persisted throttling evidence; normal minimum-interval throttling leaves signals pending for retry.

Each operation-level difference is stored in `dynamic_reschedule_changes` with old/new Work Center, old/new times, start/end shifts, time fence and frozen-conflict flag.

## Pegging and Exception Management

Full Pegging prefers the explicit Active Execution Schedule. When that run was activated by dynamic rescheduling, it adds:

- node: `RESCHEDULE_RUN`
- edge: `RESCHEDULED_BY`

Execution-management exceptions include:

- `SCHEDULE_START_LATE`
- `SCHEDULE_COMPLETION_LATE`
- `DISPATCH_BLOCKED`
- `RESCHEDULE_REQUIRED`
- `FROZEN_HORIZON_CONFLICT`
- `FIRM_HORIZON_CHANGE`

This keeps schedule changes causal: a customer-order trace can explain not only which Detailed Schedule is active, but why that execution schedule replaced its predecessor or why a candidate was rejected.

## RBAC

Read projections remain authenticated. Planner/Admin permissions protect policy creation/activation, adherence snapshot creation, manual dynamic rescheduling and explicit processing of pending signals.

## Main APIs

- `GET /api/dispatch`
- `GET /api/schedule-execution`
- `GET /api/dispatch-policy/current`
- `GET /api/dispatch-policy-versions`
- `POST /api/dispatch-policy-versions`
- `POST /api/dispatch-policy-versions/{id}/activate`
- `GET /api/schedule-adherence/current`
- `POST /api/schedule-adherence/snapshots`
- `GET /api/schedule-adherence/snapshots`
- `GET /api/schedule-adherence/snapshots/{id}`
- `POST /api/dynamic-rescheduling/run`
- `POST /api/dynamic-rescheduling/process-pending`
- `GET /api/dynamic-rescheduling/runs`
- `GET /api/dynamic-rescheduling/runs/{id}`
- `GET /api/dynamic-rescheduling/signals`

## Acceptance target

0039 runtime acceptance should prove:

1. A normal Detailed Schedule becomes the active execution schedule.
2. Dispatch List is derived from that explicit run.
3. Schedule Adherence can be snapshotted with canonical evidence.
4. New future work produces an auditable candidate and activates when frozen commitments are untouched.
5. Shop Floor execution produces a DB signal and the SYSTEM rescheduler evaluates it.
6. Full Pegging contains `RESCHEDULE_RUN` / `RESCHEDULED_BY` evidence.
7. Migration `0001 -> 0039`, dedicated E2E and the full regression suite all pass.

## Executed commitment protection
Dynamic rescheduling may calculate a candidate for impact analysis, but any candidate that changes an `EXECUTED` operation is terminalized as `BLOCKED` with execution-conflict evidence. This guarantees that only future unstarted commitments can become the new active execution schedule. Pegging exposes this as `EXECUTION_COMMITMENT_CONFLICT`.
