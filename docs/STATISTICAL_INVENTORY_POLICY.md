# 0036 Statistical Safety Stock / Inventory Policy

Migration `0036_statistical_inventory_policy.sql` adds a versioned inventory-policy layer on top of item-master fallback safety stock.

## Policy model

- Policy methods: `STATISTICAL` or `FIXED`.
- Replenishment methods: `SAFETY_STOCK` or `MIN_MAX`.
- Only one `ACTIVE` version exists per item. DRAFT configuration is mutable only before activation; ACTIVE/ARCHIVED versions are frozen audit evidence.
- Calculation runs and results are append-only snapshots so policy changes and evidence changes are distinguishable.

## Statistical formula

For target service level `SL`, `z = Phi^-1(SL)`. With average daily demand `muD`, population standard deviation `sigmaD`, average lead time `muL`, and lead-time standard deviation `sigmaL`:

`SafetyStock = z * sqrt(muL * sigmaD^2 + muD^2 * sigmaL^2)`

`ReorderPoint = muD * muL + SafetyStock`

`Min = ReorderPoint`

For `MIN_MAX`, `Max = ReorderPoint + muD * orderCycleDays`.

Demand statistics use daily `ISSUE` ledger history including zero-demand days. Purchased items use 0035 Supplier Lead-Time Reliability where qualified evidence exists; blocked suppliers are excluded. Item-master lead time remains the conservative fallback/floor.

## Planning integration

- **MRP**: `SAFETY_STOCK` keeps legacy safety-stock netting. A calculated `MIN_MAX` policy triggers only when projected stock is below ROP, then orders up to Max with the existing lot-sizing rule.
- **ATP / Order Promising**: safety stock is removed from sellable ATP and cannot be promised to new customer demand.
- **CTP**: component feasibility uses the same effective safety-stock policy.
- **Full Pegging / Exception Management**: inventory nodes show gross vs policy-protected availability and link to `INVENTORY_POLICY` root nodes. Breaches create `SAFETY_STOCK_BREACH` or `REORDER_POINT_BREACH` exceptions.

## Safety / audit properties

- Planner/admin actor validation is enforced in both API RBAC and DB triggers.
- Version activation is serialized on the item row.
- Calculation runs use `REPEATABLE READ` and a canonical SHA-256 result hash.
- Active policy calculation has a conservative item-master fallback until the first calculation completes.
