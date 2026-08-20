# Advanced Order Promising / Capable-to-Promise (0032)

0032 connects formal Sales Orders to ATP, material feasibility and finite-capacity Detailed Scheduling.

## Rules

- `POST /api/sales-orders/{id}/promise/check` is a What-if calculation.
- A check may append immutable `order_promise_*` evidence only. It does **not** create or modify WO, PO, inventory transactions, MRP plans, or detailed schedule snapshots.
- ATP excludes the current Sales Order's own lines, and same-item lines share one ATP pool.
- Sellable ATP/CTP stock is backed only by lots with `quality_status='OK'`; BLOCKED-supplier receipts are excluded.
- ATP shortages fall through to material CTP and then the same finite Detailed Scheduling allocator used by 0030.
- Promise acceptance re-runs the calculation and compares a canonical SHA-256 result hash. Changed supply/capacity returns `409 STALE_PROMISE`.
- Incomplete promises cannot be accepted.
- Accepted promise dates are written to Sales Order lines/header only inside a guarded `cpim.promise_accept` transaction and reconciled against immutable acceptance evidence at commit.

## Evidence

- `order_promise_runs`
- `order_promise_line_results`
- `order_promise_confirmations`
- `order_promise_acceptances`

## RBAC

- planner/admin: check and accept promises
- operator/viewer: read-only promise history

## Scope intentionally deferred

Backorder reprioritization, product allocation/customer priority, alternative product/plant, automatic WO/PO creation, and operation-level material pegging are not part of 0032.
