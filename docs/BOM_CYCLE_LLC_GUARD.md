# BOM Cycle / LLC Transaction Guard

## Purpose

BOM topology changes must never leave the database in a state where the BOM is cyclic or `items.low_level_code` no longer matches the committed BOM.

This version makes the following sequence atomic:

`BOM topology lock -> BOM mutation -> cycle validation -> LLC recompute -> COMMIT`

If cycle validation or LLC recomputation fails, the BOM mutation is rolled back.

## Direct BOM editing

`BOMService.Add` and `BOMService.Delete` now own BOM writes. The repository no longer exposes normal BOM write methods. Both operations begin a database transaction and obtain a PostgreSQL transaction advisory lock keyed by `cpim-mes:bom-topology`.

The global lock prevents a race such as two concurrent requests attempting `A -> B` and `B -> A`. Without serialization, each request could validate against the old graph and both could commit. With the advisory lock, one transaction completes first and the second validates against the newly committed graph and is rejected with HTTP 409.

## Cycle detection

After the proposed mutation is present inside the transaction, the complete directed BOM graph is checked using DFS. A detected cycle returns a conflict such as:

`BOM cycle detected; change rejected: <A> -> <B> -> <C> -> <A>`

No mutation is committed in this case.

## LLC recomputation

`recompute_low_level_codes()` is executed inside the same transaction after cycle validation. Its error is returned, never ignored. Therefore an LLC failure rolls back the preceding BOM write.

Migration `0019_bom_cycle_guard.sql` also replaces the old hard-coded 100-iteration limit. A valid BOM may be deeper than 100 levels, so the new bound is derived from the number of items. The function calls `assert_bom_acyclic()` before changing LLC values.

## ECO Apply

ECO Apply now performs all of the following in one transaction:

1. acquire the shared BOM topology advisory lock;
2. lock and re-read the ECO;
3. apply every ADD / REMOVE / MODIFY;
4. validate the final BOM graph;
5. recompute LLC;
6. change the ECO to `APPLIED`;
7. commit.

The final graph is checked after all ECO component operations, so an atomic replacement that temporarily changes topology is allowed as long as the final BOM is acyclic.

`REMOVE` and `MODIFY` must affect exactly one existing BOM row. An invalid ECO therefore cannot silently become `APPLIED`.

## Database defense in depth

Migration `0019_bom_cycle_guard.sql` adds:

- a BOM topology advisory-lock trigger;
- `assert_bom_acyclic()`;
- a deferred cycle constraint trigger that validates the final transaction state;
- the hardened LLC function.

This protects direct maintenance SQL and future code paths in addition to the API service checks.

## Existing database upgrade

The backend Automatic Migration Manager now detects and applies migration `0019_bom_cycle_guard.sql` before the API starts. Manual `/docker-entrypoint-initdb.d` execution is no longer required.

The migration deliberately validates existing BOM data. If a legacy cycle already exists, migration aborts instead of hiding or deleting it. Correct the cycle explicitly, then rerun the migration.

## Verification

Run:

```bash
cd backend
python3 scripts/check_bom_integrity.py
go test ./internal/service
```

The current source archive still inherits the original project issue that `go.sum` is absent, so `go test` requires dependency resolution first.
