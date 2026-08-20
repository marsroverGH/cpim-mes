# ECO Effective Date / Approval / Application Audit Guard

## Purpose

This revision makes ECO approval and application auditable and non-bypassable at both the Backend and PostgreSQL layers.

## State model

- New ECOs are always `DRAFT`.
- `DRAFT -> APPROVED` is allowed only through an authenticated admin approval.
- `APPROVED -> APPLIED` is allowed only on/after `effective_date` in the ECO business timezone.
- `DRAFT -> CANCELLED` and `APPROVED -> CANCELLED` are allowed for admin users.
- `APPLIED` and `CANCELLED` are terminal.

## Actor identity

`requestedBy`, `approvedBy`, `appliedBy`, and `cancelledBy` are not trusted from request bodies. Backend handlers derive the actor from verified JWT claims. Approval/application/cancellation also persist the immutable `users.id`.

For approval/application/cancellation PostgreSQL verifies that the actor ID exists, matches the stored username, is active, and currently has the `admin` role.

## Effective-date rule

`0023_eco_effective_date_audit.sql` defines `eco_business_date(ts)`. The timezone is read from the PostgreSQL session setting `app.business_timezone`; if absent it defaults to `Asia/Tokyo`.

Application requires:

`eco_business_date(applied_at) >= effective_date`

The Backend queries the same DB function before changing the BOM, so UI/backend/DB cannot disagree about whether the date has arrived.

## Content freeze

Approval locks the approved content:

- ECO header business fields become immutable after leaving `DRAFT`.
- `eco_components` can only be inserted/updated/deleted while the parent ECO is `DRAFT`.
- Component addition and approval both lock the ECO row, preventing a race where a component is added while approval is occurring.

## Audit fields

`engineering_changes` now stores:

- `requested_by_user_id`
- `approved_by`, `approved_by_user_id`, `approved_at`
- `applied_by`, `applied_by_user_id`, `applied_at`
- `cancelled_by`, `cancelled_by_user_id`, `cancelled_at`

Transition timestamps are written with PostgreSQL `now()` inside the same transaction as the state change.

## Append-only history

`eco_status_history` records every new ECO and status transition with:

- from/to status
- actor user ID and username
- occurrence timestamp
- effective-date snapshot
- audit source

UPDATE and DELETE of history rows are rejected by a trigger.

`GET /api/eco/{id}/history` exposes this history to authenticated users.

## Legacy rows

0023 reconstructs history where possible and labels it `LEGACY_RECONSTRUCTED`. It never invents a current user identity when the old data cannot prove one. Such legacy APPROVED rows are visible as inconsistent and cannot be applied by the new service until reconciled.

If an already-APPLIED legacy ECO has an `applied_at` date earlier than its `effective_date`, migration stops and requires explicit reconciliation.

Use:

```sql
SELECT *
FROM v_eco_audit_reconciliation
WHERE NOT is_consistent;
```

## Migration

Existing databases must apply:

`backend/migrations/0023_eco_effective_date_audit.sql`
