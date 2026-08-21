# Maintenance + Capacity Downtime (0037)

Migration `0037_maintenance_capacity_downtime.sql` makes equipment/Work Center downtime a first-class finite-capacity planning input.

## Scope

Four event types use one auditable model:

- `PREVENTIVE_MAINTENANCE`
- `BREAKDOWN`
- `PLANNED_DOWNTIME`
- `UNPLANNED_DOWNTIME`

An event owns immutable identity (`maintenance_events`). Every schedule/status/resource change is an append-only `maintenance_event_revisions` row. Current effective capacity uses the latest `PLANNED` or `ACTIVE` revision. `COMPLETED` and `CANCELLED` are terminal.

## Capacity semantics

Each revision has an exact `[start_at,end_at)` interval plus `unavailable_machines` and `unavailable_workers`. The values are validated against the Work Center's resource counts.

Detailed Scheduling subtracts those resources at interval boundaries instead of converting downtime into an all-day holiday. This means:

- a full machine outage pauses an operation until capacity returns;
- partial machine outage keeps the remaining lanes usable;
- worker reduction is enforced independently;
- long operations can split into fragments before/after maintenance;
- available machine-minutes in detailed load reporting exclude downtime.

The same allocator is reused by CTP (`SimulateCTPOrder`), so customer promise capacity and persisted Detailed Scheduling cannot disagree about maintenance availability.

## Immutable schedule evidence

Every persisted Detailed Scheduling run freezes all effective maintenance revisions in `detailed_schedule_maintenance_snapshots`. Revisions created later do not rewrite historical scheduling evidence.

An UNSCHEDULED batch retains its primary Work Center as causal evidence even when no feasible segment could be allocated.

## Full Pegging / Exception Management

0034 Full Pegging reads the maintenance snapshot attached to the referenced Detailed Scheduling run and creates:

- `MAINTENANCE_EVENT` nodes;
- `CAPACITY_REDUCED_BY` edges from Work Center nodes;
- maintenance-specific root-cause exceptions:
  - `PREVENTIVE_MAINTENANCE_CAPACITY`
  - `BREAKDOWN_CAPACITY`
  - `PLANNED_DOWNTIME_CAPACITY`
  - `UNPLANNED_DOWNTIME_CAPACITY`

Breakdown/unplanned downtime is classified as CRITICAL; planned/preventive downtime is WARNING when it causes late/partial/unscheduled capacity.

## Permissions

Planner/Admin may create events and append revisions (`maintenance.manage`). Operator/Viewer are read-only.

## API

- `GET /api/maintenance-events`
- `GET /api/maintenance-events/{id}`
- `POST /api/maintenance-events`
- `POST /api/maintenance-events/{id}/revisions`
- existing Detailed Scheduling/Order Promising/Pegging APIs automatically consume the downtime model.

## UI

`/maintenance` supports event registration, activation, completion/cancellation and revision history. Detailed Scheduling has a Maintenance tab showing the exact revisions frozen into each schedule run.
