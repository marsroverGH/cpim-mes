# PO Partial Receipts — Partial Receipt / Idempotency / MRP / ATP

## Goal

A purchase order can be received in multiple deliveries without over-receipt or duplicate stock posting.
The remaining open quantity is the only quantity treated as future scheduled supply.

Example:

- Ordered: 100
- Receipt 1: 20 -> received 20 / remaining 80 / `PARTIALLY_RECEIVED`
- Receipt 2: 30 -> received 50 / remaining 50 / `PARTIALLY_RECEIVED`
- Receipt 3: 50 -> received 100 / remaining 0 / `RECEIVED`

## Atomic receipt transaction

`POST /api/purchase-orders/{id}/receive`

```json
{
  "receiptId": "<UUID idempotency key>",
  "quantity": 20,
  "lotNo": "SUPPLIER-LOT-001"
}
```

The backend executes one database transaction:

1. advisory-lock `receiptId`
2. `SELECT purchase_orders ... FOR UPDATE`
3. check existing `purchase_receipts` for idempotent retry
4. calculate remaining quantity and reject over-receipt
5. post unified `inventory_txns` RECEIPT + mandatory `lot_movements`
6. insert immutable `purchase_receipts`
7. update `purchase_orders.received_qty` and status
8. commit

Receiver identity is taken from the verified JWT, never from the request body.

## Idempotency

`receiptId` is globally unique. Re-sending the same `receiptId`, quantity, PO and lot returns the original receipt result with `idempotentHit=true`; it does not post inventory again. Reusing the ID with different business data is rejected.

## Database invariants (migration 0022)

- `received_qty = SUM(purchase_receipts.quantity)`
- `0 <= received_qty <= quantity`
- status is `OPEN`, `PARTIALLY_RECEIVED`, or `RECEIVED` according to cumulative receipt quantity (`CLOSED` may intentionally cancel an unreceived remainder)
- every API `purchase_receipts` row must link to the exact positive unified inventory RECEIPT and lot allocation
- every `PO:<po>:RCPT:<receiptId>` inventory transaction must be linked back by exactly one purchase receipt before commit
- receipt rows and their bound inventory/lot ledger rows are immutable

Existing pre-0022 fully received POs are reconstructed from the old `PO:<poNo>` unified ledger receipt. Migration stops if the legacy receipt cannot be reconstructed unambiguously.

## MRP and ATP

For `OPEN` and `PARTIALLY_RECEIVED` POs only:

`scheduled receipt = ordered quantity - received quantity`

`RECEIVED` and `CLOSED` POs contribute zero future scheduled supply.

The quantity already received is immediately represented in physical on-hand through the unified Inventory/Lot ledger, so it is not double-counted as future supply.

## UI

The Purchase Orders screen displays ordered / received / remaining quantities, accepts a partial receipt quantity, generates a `receiptId`, and provides immutable receipt history including lot and receiving user.
