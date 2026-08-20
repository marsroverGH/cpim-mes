# Sales Order / Customer Order Management

Migration `0031_sales_order_management.sql` promotes customer demand from legacy `demand_forecasts(source='ORDER')` rows into formal Sales Orders.
During legacy reconstruction, overdue rows use `LEAST(created_at::date, due_date)` as the reconstructed Sales Order `order_date`, preserving the original audit timestamps while satisfying the `promised_date >= order_date` invariant.

## State model

`DRAFT -> CONFIRMED -> PARTIALLY_SHIPPED -> SHIPPED`

`DRAFT`, `CONFIRMED`, and `PARTIALLY_SHIPPED` can be cancelled. `SHIPPED` and `CANCELLED` are terminal.

## Canonical demand

After 0031, `demand_forecasts` is legacy read-only. Existing `ORDER` rows are reconstructed once as confirmed `LEGACY-*` Sales Orders. New committed customer demand comes from Sales Order lines.

- Forecast history: non-cancelled confirmed/shipped Sales Order quantity before the forecast as-of date.
- Forecast Consumption: current open quantity from `CONFIRMED`/`PARTIALLY_SHIPPED` lines.
- ATP committed-out: the same open quantity bucketed by promised date (requested date fallback).
- MRP remains driven by MPS; Sales Orders reach MRP through Forecast Consumption -> MPS or other explicit MPS planning.

## Allocation and shipment

Allocation is logical inventory reservation:

1. lock Sales Order + line;
2. lock item row;
3. recompute `v_stock_balance.available`;
4. insert `inventory_txns.RESERVE`;
5. append immutable allocation event;
6. increment line `allocated_qty`;
7. commit atomically.

`allocationId` is client-generated UUID idempotency.

Shipment requires allocated quantity. The service atomically:

1. locks order/line;
2. posts physical FIFO `ISSUE` via `InventoryLedgerService` (only quality `OK` lots);
3. posts matching logical `UNRESERVE`;
4. appends `SHIP_RELEASE` allocation evidence;
5. appends immutable shipment evidence;
6. updates shipped/allocated quantities;
7. transitions order to `PARTIALLY_SHIPPED` or `SHIPPED`;
8. commits.

`shipmentId` is idempotent. A repeated request with the same ID and same line/quantity returns the existing result; reuse with different parameters is rejected.

## Cancellation

Cancelling releases every remaining reservation and moves the unshipped remainder to `cancelled_qty` in one transaction. Partial shipments remain historical facts.

## Database defense in depth

0031 adds:

- valid Sales Order status transitions;
- BLOCKED-customer confirm/create guards;
- post-confirmation master-field immutability;
- append-only status/allocation/shipment evidence;
- deferred reconciliation of line `allocated_qty` vs allocation events;
- deferred reconciliation of line `shipped_qty` vs shipment rows;
- shipment <-> unified inventory ISSUE exact matching;
- allocation event <-> RESERVE/UNRESERVE exact matching;
- immutability of inventory transactions referenced by Sales Order evidence;
- header state reconciliation (`PARTIALLY_SHIPPED`, `SHIPPED`, `CANCELLED`);
- legacy `demand_forecasts` read-only guard.

## RBAC

- `viewer`: read only.
- `operator`: shipment execution only.
- `planner`: customer/order management, confirm/cancel, allocation/release, shipment.
- `admin`: all permissions.

## API

- `GET/POST /api/customers`
- `PUT /api/customers/{id}`
- `GET/POST /api/sales-orders`
- `GET /api/sales-orders/{id}`
- `POST /api/sales-orders/{id}/confirm`
- `POST /api/sales-orders/{id}/cancel`
- `POST /api/sales-order-lines/{id}/allocate`
- `POST /api/sales-order-lines/{id}/release-allocation`
- `POST /api/sales-order-lines/{id}/ship`

## UI

`/sales-orders` provides customer management, DRAFT order entry, confirm/cancel, allocation/release, shipment, status history and shipment history. The legacy `/demand` screen is read-only after 0031.
