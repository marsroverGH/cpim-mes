# Full Pegging + Exception Management (0034)

0034 adds an auditable, immutable supply-demand causal graph on top of Sales Orders, Advanced Order Promising (0032), Backorder Processing/Product Allocation (0033), MRP, Purchasing, Supplier Quality, Lot Quality and Detailed Scheduling.

## Purpose

The objective is to answer, from a Sales Order, both:

1. **What supply is this demand relying on?**
2. **Why is the order late, backordered or at risk?**

A pegging snapshot traces from the Sales Order and each open line into accepted Promise/BOP evidence, usable inventory, Work Orders, BOM requirements, Purchase Orders, supplier/quality constraints and finite-capacity Detailed Scheduling evidence.

## Immutable Pegging Snapshot

`POST /api/sales-orders/{id}/pegging/run` creates a `pegging_runs` snapshot under `REPEATABLE READ`. The run has a bounded planning horizon and stores a SHA-256 canonical result hash.

Graph evidence is append-only:

- `pegging_nodes`
- `pegging_edges`
- `planning_exceptions`

After a run reaches `SUCCEEDED` or `FAILED`, its request/header data cannot be rewritten. Child graph rows can only be inserted while the run is `RUNNING`.

## Graph Nodes

The graph can contain:

- `SALES_ORDER`
- `SALES_ORDER_LINE`
- `PROMISE`
- `BACKORDER`
- `INVENTORY`
- `ITEM`
- `WORK_ORDER`
- `PLANNED_ORDER`
- `PURCHASE_ORDER`
- `SUPPLIER`
- `QUALITY_HOLD`
- `DETAILED_SCHEDULE`
- `WORK_CENTER`
- `SHORTAGE`

Edges express the causal/supply relationship, including `HAS_LINE`, `PROMISED_BY`, `REPRIORITIZED_BY`, `ALLOCATED_FROM`, `SUPPLIED_BY`, `REQUIRES_COMPONENT`, `PURCHASED_BY`, `PLANNED_SUPPLY`, `SCHEDULED_BY`, `USES_WORK_CENTER`, `BLOCKED_BY` and `SHORT_BY`.

## Promise / BOP Decision Lineage

If both an accepted 0032 Promise and a published 0033 BOP decision exist for the same Sales Order line, the graph preserves both pieces of historical evidence but marks only the newer planning decision as `EFFECTIVE`; the older decision is `SUPERSEDED`. A backorder exception is raised only from the effective BOP decision, preventing a later Promise Acceptance from being obscured by stale BOP evidence.

## No Double Pegging

Inventory, Work Order remaining quantity, Purchase Order remaining quantity and MRP planned supply are maintained as shared run-local supply pools. Each quantity is decremented as it is pegged, so the same formal supply cannot be credited to multiple demands in the same run.

The selected formal supply is also bounded by `horizon_days`. A supply receipt outside the run horizon is not used as a causal basis for the current snapshot.

## BOM and Manufacturing Trace

For released/in-progress Work Orders, 0034 prefers `work_order_bom_snapshot_lines`, preserving the material requirements that were actually frozen at WO release. Live `bom_components` are used only when no released snapshot exists or for hypothetical/latest MRP planned supply.

Component demand recursively traces through:

- usable lot-backed inventory;
- quality HOLD quantity;
- approved Purchase Orders;
- supplier qualification status;
- child FG/SA Work Orders;
- shortages.

Recursion is bounded to prevent accidental infinite graph growth even if corrupt legacy data exists.

## Capacity Root Cause

Formal Work Orders are linked to the most recent completed Detailed Scheduling evidence. The graph includes the schedule order and Work Centers used by its batches.

The engine detects:

- missing finite schedule evidence;
- `UNSCHEDULED`/`PARTIAL` schedule status;
- finite-capacity finish later than the required date.

This makes the Work Center or schedule record visible as a root cause rather than reporting only a generic late date.

## Exception Types

0034 detects these initial exception families:

- `LATE_PROMISE`
- `BACKORDER`
- `UNCONVERTED_CTP`
- `MATERIAL_SHORTAGE`
- `LATE_PURCHASE_ORDER`
- `SUPPLIER_BLOCKED`
- `QUALITY_HOLD`
- `LATE_WORK_ORDER`
- `CAPACITY_LATE`
- `CAPACITY_UNSCHEDULED`

Each exception stores severity, impact date/days, a root node and a JSON root-cause path that begins from Sales Order demand and ends at the detected constraint.

## Exception Lifecycle

Detection evidence itself is immutable. Planner/Admin workflow actions are stored separately in `planning_exception_actions`:

- `ACKNOWLEDGE`: `OPEN -> ACKNOWLEDGED`
- `RESOLVE`: `OPEN/ACKNOWLEDGED -> RESOLVED`
- `REOPEN`: `RESOLVED -> OPEN`

Actions are append-only, serialized with a PostgreSQL advisory transaction lock, and DB-validated against an active matching planner/admin user.

`v_current_planning_exceptions` exposes only the exceptions from the latest successful pegging snapshot for each Sales Order while historical runs remain available for audit and forensics.

## API

- `POST /api/sales-orders/{id}/pegging/run`
- `GET /api/sales-orders/{id}/pegging-runs`
- `GET /api/pegging-runs/{id}`
- `POST /api/planning-exceptions/scan`
- `GET /api/planning-exceptions?status=&severity=&type=`
- `POST /api/planning-exceptions/{id}/actions`

Pegging runs and exception mutation require planner/admin permissions. Authenticated users may read history/dashboard data.

## UI

`/pegging-exceptions` provides two operational views:

1. **Exception Dashboard** — scan committed orders, filter by status/severity/type, inspect root-cause paths, acknowledge/resolve/reopen.
2. **Sales Order Pegging** — run a Sales Order snapshot, inspect graph nodes, causal edges and detected root-cause exceptions, and reopen historical runs.

## Acceptance Criteria

0034 is considered complete when:

- migrations `0001..0034` apply from an empty database;
- migration ledger contains exactly 34 sequential rows;
- all existing static guards plus `check_full_pegging_exception_management.py` pass;
- Go tests and frontend typecheck/build pass;
- `pegging-exceptions.spec.ts` passes;
- the complete Playwright suite passes serialized;
- `git diff --check` is clean.

## Deliberate Limitations

0034 is explanatory and auditable; it does not automatically reschedule Work Orders, expedite Purchase Orders, unblock suppliers, release quality HOLD or change BOP/Product Allocation policy. Those remain explicit planner/execution decisions. A later exception-policy layer can add recommended actions and automated alerts without weakening 0034's immutable evidence model.
