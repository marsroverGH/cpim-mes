# Transactional Quality Management

Migration `0024_quality_transaction_audit.sql` makes a quality inspection a single atomic business event instead of two independent writes.

## Atomic transaction

The backend locks the target lot (`SELECT ... FOR UPDATE`) and inserts the inspection inside one transaction. PostgreSQL triggers then, in that same transaction:

1. validate the authenticated inspector identity,
2. capture the lot's previous quality status,
3. derive the new status from `PASS / HOLD / FAIL`,
4. update `lots.quality_status`, and
5. append one immutable `quality_status_history` row.

If any step fails, the inspection, lot state, and audit history all roll back together.

## Identity and timestamps

The API accepts only `result`, `defectQty`, and `notes`. `inspector`, `inspector_user_id`, and `inspected_at` are server/database authoritative. The HTTP layer derives the actor from the currently authenticated JWT, and the DB verifies that the user still exists, is active, and has role `operator`, `planner`, or `admin`.

## State mapping

- `PASS -> OK`
- `HOLD -> HOLD`
- `FAIL -> REJECTED`

`HOLD` and `REJECTED` lots remain excluded from normal FIFO consumption by the unified inventory/lot ledger.

## Immutable evidence

`quality_inspections` and `quality_status_history` are append-only after migration 0024. A correction must be represented by a new inspection; existing evidence cannot be edited or deleted.

Direct updates of `lots.quality_status` are blocked. Quality state changes must originate from the inspection transaction, preventing SQL or application paths from changing the lot without matching inspection evidence.

## Legacy data

Existing inspections are backfilled with resulting statuses and a `LEGACY_RECONSTRUCTED` history row. Inspector user IDs are filled only when the old username can be matched to an existing user. The first historical transition may have unknown `from_status` because pre-0024 state changes were not audited.

If a lot's current status disagrees with its latest historical inspection, migration 0024 stops instead of silently rewriting production quality history.

## API

- `GET /api/lots/{id}/inspections`
- `POST /api/lots/{id}/inspections`
- `GET /api/lots/{id}/quality-history`
- `GET /api/quality/recent`

The POST endpoint is protected by backend permission `quality.record`.
