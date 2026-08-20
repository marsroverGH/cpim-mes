# Strict Shop Floor State Machine (0021)

## Purpose

This change makes Shop Floor execution sequential, transactional and auditable.
A client can no longer start an arbitrary downstream operation, spoof the operator
name, or split the operation status update from its event log.

## Operation states

```text
PENDING -> READY -> IN_PROGRESS <-> PAUSED -> COMPLETED
```

Rules:

- Release creates the first routing operation as `READY` and all successors as `PENDING`.
- `START` is allowed only from `READY` or `PAUSED`.
- `STOP` is allowed only from `IN_PROGRESS` and changes the state to `PAUSED`.
- A cumulative good-quantity report is allowed only from `IN_PROGRESS`.
- A partial good-quantity report leaves the operation `IN_PROGRESS`.
- When cumulative good quantity reaches the WO planned quantity, the operation becomes `COMPLETED` and the next operation becomes `READY` in the same transaction.
- A `COMPLETED` operation cannot be reopened by the normal Shop Floor API.
- A later sequence cannot start until every lower sequence is `COMPLETED`.

## Atomic execution

Every `START`, `STOP` and `COMPLETE` follows the same lock order:

```text
BEGIN
  work_orders ... FOR UPDATE
  wo_operations ... FOR UPDATE
  validate state / predecessor
  update wo_operations
  insert operation_logs
  promote next step if needed
COMMIT
```

If log insertion, state validation or next-step promotion fails, the entire action
is rolled back.

## Authenticated operator identity

The API ignores/removes client-supplied operator identity. The service receives:

- `user_id`
- current `username`

from the JWT claims already refreshed against the `users` table by
`VerifyCurrent`. New operation logs store both the username snapshot and
`operator_user_id`. A DB trigger verifies that the two values match the current
`users` row.

## Server-side elapsed time

`START` sets `active_started_at` using backend time. `STOP` and `COMPLETE` add the
elapsed interval to `actual_minutes` and close/reset the active session. Client
`addMinutes` values are no longer accepted as the authoritative actual time.

## Database protection

Migration `0021_shop_floor_state_machine.sql` adds:

- `READY` and `PAUSED` states;
- `active_started_at`;
- `operator_user_id` foreign keys;
- one-active-step-per-WO partial unique index;
- transition trigger;
- deferred sequence constraint trigger;
- authenticated operation-log actor trigger.

The migration deliberately stops if legacy data already contains a started or
completed operation after an unfinished predecessor, or multiple active
operations for one WO. Reconcile those rows before retrying; the migration does
not silently rewrite historical execution facts.

## Deployment

The backend Automatic Migration Manager applies migrations through `0021` in order before serving API requests.
After migration, restart backend and frontend together because the Shop Floor
request bodies no longer carry `operator` or `addMinutes`.
