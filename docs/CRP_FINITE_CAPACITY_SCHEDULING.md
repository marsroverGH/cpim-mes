# CRP Finite-Capacity Scheduling

## Purpose

The CRP engine now produces a finite-capacity operation schedule rather than only reporting overload. Capacity is reserved by work center and actual clock interval, so two operations cannot occupy the same aggregate work-center interval in one schedule snapshot.

## Scheduling policy

1. `RELEASED` / `IN_PROGRESS` work orders are loaded first as **FIRM_WO**.
2. Remaining MRP planned orders are loaded as **MRP_PLANNED**, sorted by due date.
3. Routing operations are scheduled in sequence using forward finite scheduling.
4. Each operation starts no earlier than its order release/start constraint and no earlier than completion of its predecessor.
5. If an operation does not fit in one workday it is split across subsequent workdays.
6. Holidays and zero-capacity days are skipped.
7. Work-center efficiency and utilization reduce effective load capacity.
8. If the horizon ends before all load can be placed, the order is `PARTIAL` or `UNSCHEDULED`.
9. If all load is scheduled but completion is after the due date, the order is `LATE`.

## Work-center clock model

`shift_start_minute` defines the local workday start (default 480 = 08:00).

The calendar provides available clock minutes. `capacity_minutes_per_day`, efficiency and utilization are converted into an effective load rate for that clock window. A work center with aggregate/parallel capacity greater than the calendar window can therefore process more than one standard load minute per clock minute while still producing a non-overlapping aggregate schedule timeline.

## Immutable schedule snapshots

Migration `0029_crp_finite_capacity_scheduling.sql` adds:

- `crp_schedule_runs`
- `crp_schedule_orders`
- `crp_schedule_segments`

A run is built in `BUILDING` status and becomes immutable after `COMPLETE`. PostgreSQL deferred validation rejects overlapping segments on the same work center and verifies that each order's scheduled minutes equal its segment total.

## APIs

- `POST /api/crp/schedule` — create a finite schedule snapshot.
- `GET /api/crp/schedule-runs` — list recent snapshots.
- `GET /api/crp/schedule-runs/{id}` — reload one snapshot.
- `POST /api/crp/run` — retained as the legacy/infinite-capacity load comparison.

## Relationship to Shop Floor

This CRP schedule is a planning snapshot. It does not rewrite actual Shop Floor START/STOP/COMPLETE timestamps. Released/in-progress WO remaining operations are treated as firm load when a new CRP schedule is generated.

## Known modeling boundary

The engine uses preemptive daily splitting: an operation may continue on the next workday, while setup time is included once in the operation's total standard load. Sequence-dependent setup matrices, alternate work centers, finite labor crews, material-availability-at-operation and transfer-batch overlap are not yet modeled.
