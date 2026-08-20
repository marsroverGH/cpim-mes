# WO Atomic Release / Concurrent Reservation Fix

## Purpose

`ReleaseWorkOrder` previously performed the WO-status check and inventory-availability check before starting the database transaction. Two concurrent requests could therefore both observe the same `PLANNED` WO or the same component availability and both create `RESERVE` rows.

This change makes WO release an atomic database workflow.

## Transaction sequence

1. `BEGIN`
2. Lock the target WO with `SELECT ... FROM work_orders ... FOR UPDATE`.
3. Re-check that the locked WO is still `PLANNED`.
4. Read the direct BOM inside the same transaction and persist an immutable WO BOM Snapshot.
5. Calculate requirements from the Snapshot, then sort component UUIDs into a deterministic order.
6. Lock every component `items` row with `SELECT ... FOR UPDATE`.
7. Only after all component locks are acquired, re-read `v_stock_balance` and calculate available stock.
8. If any component is short, roll back without creating any reservation.
9. Insert all `RESERVE` rows.
10. The BOM Snapshot, reservations, and status change remain in the same transaction.
11. Update the WO from `PLANNED` to `RELEASED` with a guarded `WHERE status='PLANNED'` clause.
12. Copy routing operations.
13. `COMMIT`.

## Concurrency behavior

### Same WO released twice

The first request obtains the WO row lock. The second request waits. After the first request commits, the second request reads `RELEASED` and returns a conflict. No second reservation is posted.

### Different WOs competing for the same component

Assume component A has 100 available units and both WO-1 and WO-2 need 80.

- WO-1 locks component A, sees 100 available, reserves 80, then commits.
- WO-2 waits on the same component row.
- After WO-1 commits, WO-2 acquires the lock and re-reads the balance.
- WO-2 sees only 20 available and is rejected for shortage.

The resulting reserved quantity cannot become 160 through concurrent WO releases.

### Multiple shared components

Component rows are always locked in sorted UUID order. This avoids the classic deadlock where one transaction locks A then B while another locks B then A.

## Defense-in-depth database constraint

Migration `0016_atomic_wo_release.sql` adds a partial unique index on `(item_id, ref_doc)` for `txn_type='RESERVE'` and `ref_doc LIKE 'WO:%'`.

This prevents accidental duplicate reservation rows for the same WO/component even if another code path attempts to post one.

If an existing database already contains duplicate WO/component `RESERVE` rows, migration 0016 intentionally fails with a clear message. Those duplicates must be reconciled before enabling the uniqueness guard; silently deleting inventory ledger rows would be unsafe.

## Scope

This fix prevents double reservation caused by concurrent **WO Release** operations. It does not yet turn the entire inventory subsystem into a single serialized ledger. Direct inventory-adjustment APIs should ultimately use the same item-locking convention when they can reduce availability.

## Validation performed for this build

- Go parser check: all backend `.go` files parse successfully.
- `gofmt` check: modified Go files are clean.
- Static workflow assertions confirm that transaction start precedes the WO read, both WO and component item `FOR UPDATE` locks are present, balance is read after component locks, the status update is guarded by `status='PLANNED'`, and reservation inserts are conflict-safe.
- Migration 0016 was checked for the duplicate preflight and partial unique index definition.
- Pure-function regression tests were added for deterministic unique component lock ordering and for the second-WO shortage after a committed competing reservation.

A full `go test ./...` cannot run in the supplied project because `backend/go.sum` is absent. Dependency download was attempted, but this execution environment cannot resolve `proxy.golang.org`. No fabricated `go.sum` has been added.
