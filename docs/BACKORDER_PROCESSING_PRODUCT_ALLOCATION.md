# Backorder Processing + Product Allocation (0033)

0033 extends Advanced Order Promising with controlled reallocation after supply, demand or capacity changes.

## Scope

- Customer Service Class: `STRATEGIC`, `STANDARD`, `OTHER` seeded as default policy classes.
- Sales Order priority: `EXPEDITE`, `HIGH`, `NORMAL`.
- Product Allocation Plans reserve percentages of scarce ATP per service class.
- Backorder Processing (BOP) ranks committed open Sales Order lines, recalculates ATP/CTP and produces immutable Before/After proposals.
- `Preview` is side-effect free for Work Orders, Purchase Orders, Inventory and Detailed Schedule snapshots.
- `Publish` revalidates the canonical result hash and updates promised dates only if the preview is still current.

## Ranking

Demand is processed in this order:

1. Sales Order priority (`EXPEDITE` before `HIGH` before `NORMAL`).
2. Customer Service Class priority rank.
3. Requested date.
4. Order date / order number / line number.

Existing physical inventory allocations are treated as fixed supply for their owning Sales Order line and are not redistributed by BOP. Product Allocation controls only the remaining scarce ATP. Any remaining quantity is passed to the existing CTP material and Detailed Scheduling capacity simulation.

## Product Allocation

A plan is created as `DRAFT`. Bucket percentages must total exactly 100% before activation. Once ACTIVE, the plan definition and buckets are immutable. Active plans for the same item may not overlap in effective dates.

0033 uses one active plan per item for the current BOP run date. The percentage is applied to the item's ATP pool over the selected BOP horizon. CTP production/purchase after ATP is not percentage-capped; it is used to move excess demand to a feasible later date instead of consuming protected ATP.

## Preview / Publish

`POST /api/backorders/preview`

- selects `CONFIRMED` / `PARTIALLY_SHIPPED` open demand;
- excludes all target lines from ATP committed demand so the pool can be reassigned once;
- protects each line's existing allocation;
- shares ATP, material usage and conservative capacity across the entire run;
- stores an immutable run, proposal lines and confirmation evidence;
- calculates a SHA-256 canonical result hash.

`POST /api/backorders/publish`

- locks affected Sales Orders and Items;
- recalculates the same run;
- returns `409 STALE_BOP` if the result hash changed;
- inserts immutable publication evidence;
- updates Sales Order line/header promised dates inside `cpim.bop_publish` DB write context.

A partially unconfirmed line has `proposed_promised_date = NULL`; the exact confirmed/backorder quantities remain in BOP evidence rather than implying that the whole line is promised.

## Promise / BOP coexistence

0032 Promise Acceptance and 0033 BOP Publication are both immutable planning decisions. Database reconciliation uses the newer decision:

- a BOP publication newer than the latest Promise Acceptance supersedes that promise's promised-date reconciliation;
- a later Promise Acceptance supersedes the older BOP publication.

Direct promised-date edits outside `OrderPromising.Accept` or `Backorder.Publish` remain blocked.

## RBAC

Planner and Admin:

- `sales-order.backorder`
- `sales-order.product-allocation`

Operator and Viewer cannot run/publish BOP or modify Product Allocation policy.

## UI

`/backorders` provides:

- BOP Preview and Before/After result table;
- Publish action;
- Customer Service Class assignment;
- Sales Order Priority assignment;
- Product Allocation Plan creation / activation / deactivation;
- confirmation/source detail.

## Deliberate limitations

0033 does not move physical inventory reservations between Sales Orders. Existing allocations are fixed. It also does not persist hypothetical CTP Work Orders, Purchase Orders or Detailed Schedule runs. Full supply-demand pegging and exception explanations remain candidates for 0034.
