# Unified Inventory / Lot Ledger (0018)

## Purpose

This change removes the old dual-write model where `inventory_txns` and
`lot_movements` could be posted independently. After migration 0018, physical
inventory exists only when the item-level transaction and its lot allocation(s)
exist together in the same database transaction.

## Ledger contract

`inventory_txns` is the canonical item-level transaction header.

For every physical transaction:

- `RECEIPT`: signed quantity is positive.
- `ISSUE`: signed quantity is negative.
- `ADJUST`: signed quantity may be positive or negative.
- The signed sum of every linked `lot_movements.quantity` must equal the exact
  `inventory_txns.quantity`.
- Every `lot_movement` must have a non-null `txn_id`.
- The lot item must equal the transaction item.
- `ref_doc` must match between header and allocation lines.
- `RECEIPT` may allocate as `RECEIPT` or `PRODUCED`.
- `ISSUE` may allocate as `ISSUE` or `CONSUMED`.
- `ADJUST` may allocate only as `ADJUST`.

`RESERVE` and `UNRESERVE` remain logical item-level transactions. They never
create lot movements because they do not move physical inventory.

## Database enforcement

Migration `0018_unified_inventory_lot_ledger.sql` runs inside one explicit database transaction and adds both immediate and
deferred PostgreSQL protection:

1. `lot_movements.txn_id` becomes `NOT NULL`.
2. Lot and inventory transaction foreign keys use `ON DELETE RESTRICT`.
3. A row trigger rejects wrong item, sign, type, and reference-document links.
4. Deferred constraint triggers verify at COMMIT that:
   `inventory transaction quantity == SUM(lot allocation quantities)`.
5. A lot's `item_id` is immutable.
6. A newly created lot cannot COMMIT without at least one ledger movement.
7. A committed lot balance may never be negative; a deferred constraint rejects
   direct SQL or application writes that would drive any lot below zero.
8. `v_stock_balance.on_hand` is calculated from lot movements, so displayed
   physical on-hand is literally the sum of lot balances.
9. `v_inventory_lot_reconciliation` compares the canonical item ledger to lot
   balances. In committed post-0018 data, `difference` must remain zero.

These constraints protect consistency even if SQL is executed outside the Go
application.

## Single application write path

`InventoryLedgerService` is now the only application service allowed to write
physical `RECEIPT`, `ISSUE`, or `ADJUST` transactions.

The following operations use it:

- Manual inventory transaction entry.
- Lot registration.
- Lot movement entry.
- PO receipt.
- WO component consumption.
- WO finished-goods receipt.
- Cycle-count variance adjustment.

`InventoryRepo.Post` rejects physical writes and is retained only for logical
`RESERVE` / `UNRESERVE` use.

## Lot allocation behavior

### Positive receipt / positive adjustment

A lot number may be supplied. If omitted, the system creates a generated lot
number. The item transaction and lot allocation commit atomically.

### Negative issue

A specific lot may be supplied. If omitted, the unified ledger performs FIFO
allocation over quality status `OK` lots while holding the item row and lot rows.

### Negative cycle-count adjustment

The adjustment may consume all physical lot statuses because it represents a
physical reconciliation rather than production consumption.

## Legacy-data migration

Pre-0018 data may contain one-sided history.

- Orphan lot movements with no `inventory_txns` parent are copied to
  `legacy_orphan_lot_movements_0018`, then removed from the active physical
  ledger. This preserves history without inventing additional canonical stock.
- A positive physical `inventory_txns` quantity that lacks complete lot allocation
  is assigned to a `MIGRATION-UNALLOCATED-*` lot with quality status `HOLD`.
- A negative missing allocation is assigned FIFO against existing positive lot
  balances. The migration never creates an artificial negative-balance lot.
- If legacy data cannot be allocated without driving a lot negative, migration
  stops and requires explicit reconciliation instead of inventing history.
- No canonical inventory quantity is deleted or silently changed.
- The migration finishes with both reconciliation and non-negative-lot assertions;
  if the item and lot ledgers still differ, migration fails.

`MIGRATION-UNALLOCATED-*` lots should be physically reconciled before production
use because their historical lot identity was not recoverable.

## API / UI

A diagnostic endpoint was added:

`GET /api/inventory/reconciliation`

The Inventory screen displays `Lot整合: OK/NG` based on this endpoint.

Manual physical inventory entry now also supports:

- `lotNo` for positive receipt/adjustment.
- `lotId` for a specific negative issue/adjustment.
- blank `lotId` for automatic FIFO allocation.

## Concurrency

Physical inventory writes lock the item row first, then lot rows. WO release uses
the same item-row locking convention. This provides a deterministic lock order and
prevents concurrent writers from using stale lot balances.

## Operational check

After migration, this query must always return zero differences for committed data:

```sql
SELECT *
FROM v_inventory_lot_reconciliation
WHERE abs(difference) > 0.000001;
```

Expected result: zero rows.
