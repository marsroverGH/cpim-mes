# Final Operation → Finished-Goods Receipt Guard

## Purpose

A work order may not post more finished-goods inventory than has actually been completed at its final Shop Floor routing operation.

Example: WO planned quantity = 100. If the final operation cumulative good quantity is 20, the WO cumulative finished-goods receipt may be at most 20. If 10 has already been received, the next completion call may receive at most 10.

## Backend guarantee

`WorkflowService.CompleteWorkOrder` locks rows in this order:

1. `work_orders ... FOR UPDATE`
2. final `wo_operations ... FOR UPDATE`
3. material/lot rows through `InventoryLedgerService`

It then validates:

`WO completed_qty + this completion quantity <= final operation completed_qty`

`ShopFloorService.Complete` uses the same lock order (`work_orders` then `wo_operations`) so a concurrent final-operation report and WO receipt cannot race around the check.

Shop Floor `completedQty` is cumulative good quantity. A partial report leaves the operation `IN_PROGRESS`; it becomes `COMPLETED` only when cumulative quantity reaches the WO planned quantity.

## DB guarantee (migration 0020)

Migration `0020_final_operation_receipt_guard.sql` adds:

- `work_order_completions.receipt_txn_id`
- FK/unique binding to the exact `inventory_txns` RECEIPT
- immediate trigger validating item, quantity, WO/completion reference, produced lot and PRODUCED lot movement
- deferred constraint triggers enforcing cumulative completion invariants at COMMIT
- `v_wo_final_operation_reconciliation`

The deferred invariant is:

- `SUM(work_order_completions.quantity) = work_orders.completed_qty`
- `work_orders.completed_qty <= final wo_operations.completed_qty`
- final operation completed quantity must be within `0..WO planned quantity`
- a WO with physical completed quantity must have a final Shop Floor operation

Because the constraint is deferred, the application can atomically create the receipt, completion history and cumulative WO update in one transaction. Direct SQL that would commit an over-receipt is rejected.

## Migration of existing databases

0020 attempts to bind existing post-0015 completion records to receipt transactions that use the `WO:<orderNo>:COMP:<completionId>` convention. Older legacy rows may remain unbound, but all new completion inserts must have a valid receipt transaction.

The migration intentionally fails if an existing WO already violates the final-operation quantity invariant. Inspect:

```sql
SELECT *
FROM v_wo_final_operation_reconciliation
WHERE NOT is_consistent;
```

Reconcile those legacy WOs before retrying the migration.

## UI behavior

The Shop Floor completion dialog now treats `completedQty` as cumulative good quantity and shows the increment for the current report. Partial cumulative quantities keep the operation `IN_PROGRESS`.

The Work Orders completion dialog loads the highest-sequence WO operation and shows both final-operation actual quantity and current receipt availability. The input maximum is the lesser of WO remaining quantity and `final operation completed_qty - WO completed_qty`.

## Verification

- Pure Go quantity-rule tests: PASS
- Final-operation static guard: PASS
- Backend RBAC route check: PASS
- BOM integrity check: PASS
- Go syntax parse: PASS
- Changed TypeScript syntax transpile: PASS

The repository still lacks `backend/go.sum`, so a normal `go test ./...` stops at dependency verification before package compilation. The DB-independent final-operation rules are tested separately with `go test final_operation_rules.go final_operation_test.go`.
