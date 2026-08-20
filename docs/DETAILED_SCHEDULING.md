# Detailed Scheduling — finite multi-resource job-shop heuristic

Migration: `0030_detailed_scheduling.sql`

This module extends finite-capacity CRP into executable detailed planning. It keeps the existing CRP snapshots for aggregate comparison and creates a separate immutable Detailed Scheduling snapshot.

## Scope

The scheduler simultaneously considers:

- primary and alternative Work Centers;
- operation overlap / lot streaming;
- Transfer Batch quantities;
- sequence-dependent setup times;
- multiple identical equipment units (machine lanes) per Work Center;
- worker head-count capacity per Work Center;
- work calendars, holidays and shift start times;
- Work Center efficiency and utilization;
- Routing precedence and partially completed firm WOs;
- firm `RELEASED` / `IN_PROGRESS` WOs before MRP planned orders.

The implementation is a deterministic finite job-shop **heuristic**, not a mathematical proof of global optimality. Each transfer batch is assigned to the feasible candidate that produces the earliest completion; alternative priority is the tie breaker. This keeps planning explainable and usable without adding an external optimization runtime.

## Master data

### Work Center

`work_centers` adds:

- `machine_count`: number of parallel equipment units;
- `worker_count`: simultaneous labor head-count available;
- existing `capacity_minutes_per_day` is interpreted by Detailed Scheduling as clock minutes **per equipment unit per workday**;
- existing calendar and `shift_start_minute` define the working window.

A reduction in machine/worker capacity is rejected if it would make an existing routing master requirement invalid.

### Routing Operation

`routing_operations` adds:

- `setup_family`;
- `overlap_enabled`;
- `transfer_batch_qty`;
- `machines_required`;
- `workers_required`.

When `overlap_enabled=true`, `transfer_batch_qty` must be greater than zero.

### Alternative Work Center

`routing_operation_alternatives` contains:

- alternative Work Center;
- priority;
- run-time multiplier;
- setup-time multiplier;
- active flag.

The same Work Center cannot be both primary and alternative. The alternative must have enough equipment and workers for the operation requirement.

### Sequence-dependent Setup Matrix

`work_center_setup_matrix` maps:

`(Work Center, From Setup Family, To Setup Family) -> setup minutes`

Wildcards are supported with `*`.

Lookup order is:

1. exact `from -> to`;
2. `* -> to`;
3. `from -> *`;
4. `* -> *`;
5. routing operation base setup time.

A same-family transition has zero sequence setup.

## WO release snapshot

Detailed-routing execution parameters are copied into `wo_operations` at WO Release. Active alternatives are copied into `wo_operation_alternatives` in the same transaction.

Therefore later Routing/ECO/master changes do not silently change an already-released WO's executable resource requirements.

Only the active Routing is copied at release.

## Transfer Batch and overlap

Example:

- WO quantity = 100
- Operation 10 transfer batch = 20
- Operation 20 overlap enabled

Operation 20 can become `READY` after Operation 10 has physically completed 20 units; it no longer has to wait for all 100 units.

The downstream cumulative good quantity can never exceed the immediate predecessor's cumulative completed quantity.

Without overlap, the legacy behavior remains: the next operation waits until the predecessor is fully `COMPLETED`.

## Scheduling algorithm

1. Load Work Centers and calendar windows.
2. Create one machine lane per `machine_count`.
3. Load the setup matrix.
4. Load firm WOs first; use only remaining quantities for unfinished operations.
5. Load MRP planned orders next.
6. Split overlapping operations into transfer batches.
7. Build batch dependencies:
   - previous transfer batch in the same operation;
   - the predecessor routing batch that supplies the required cumulative quantity.
8. For each candidate Work Center:
   - check machine and worker resource feasibility;
   - select available machine lane(s);
   - calculate sequence-dependent setup;
   - apply alternative setup/run multipliers;
   - apply efficiency/utilization to run clock time;
   - find calendar/worker-feasible clock fragments.
9. Choose the candidate with earliest completion; priority breaks ties.
10. Reserve machine lanes and worker head-count.
11. Persist an immutable schedule snapshot.

An already-started firm operation stays on its released primary Work Center; it is not moved to an alternative after execution has begun.

## Equipment and labor constraints

`machines_required=N` means N machine lanes must be available simultaneously for the whole setup/run segment. The duration is not divided by N; the N machines are a simultaneous resource requirement.

`workers_required=N` reserves N workers in that Work Center. The sum of overlapping segment worker requirements may not exceed `worker_count`.

The database revalidates both machine-lane overlap and cumulative worker head-count before commit.

## Snapshot tables

- `detailed_schedule_runs`
- `detailed_schedule_orders`
- `detailed_schedule_batches`
- `detailed_schedule_batch_dependencies`
- `detailed_schedule_segments`
- `detailed_schedule_machine_allocations`

Completed snapshots are immutable.

Database constraints verify:

- segment/batch identity;
- machine lane allocation count;
- no overlapping use of a machine lane;
- machine lane number <= machine capacity snapshot;
- worker head-count <= worker capacity snapshot;
- routing/transfer dependency precedence;
- scheduled batch start/end equals its segment range;
- schedule horizon.

## API

- `POST /api/detailed-scheduling/run`
- `GET /api/detailed-scheduling/runs`
- `GET /api/detailed-scheduling/runs/{id}`
- `GET/POST /api/routing-operations/{opId}/alternatives`
- `DELETE /api/routing-operation-alternatives/{id}`
- `GET/POST /api/work-centers/{id}/setup-matrix`
- `DELETE /api/work-center-setup-matrix/{id}`

The run endpoint requires `planning.crp.run`. Routing and Work Center master changes keep their existing master-data permissions.

## UI

- `/detailed-scheduling`: run/history, orders, transfer batches, segments and load views;
- `/work-centers`: machine count, worker count and sequence setup matrix;
- `/routings`: setup family, overlap, transfer batch, machine/worker requirements and alternative Work Centers.

## Relationship to CRP and Shop Floor

Detailed Scheduling does **not** overwrite Shop Floor actual timestamps. It is a planning snapshot.

On the next run, `RELEASED` and `IN_PROGRESS` WOs are re-read as firm load using their remaining operation quantities, while Shop Floor continues to own actual execution state and cumulative good quantity.

The older `/crp/schedule` remains available as an aggregate finite-capacity comparison. Detailed Scheduling is the higher-fidelity planning layer.

## Known boundary

This version uses a deterministic heuristic rather than CP-SAT/MILP. It does not claim a globally optimal makespan or tardiness objective. Future optimization can reuse the same master/snapshot schema and replace the assignment engine with CP-SAT while preserving API/audit semantics.
