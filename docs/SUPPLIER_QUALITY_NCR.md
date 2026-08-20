# Supplier Quality / NCR

Migration: `0027_supplier_quality_ncr.sql`

## Purpose

Supplier Quality extends incoming inspection into an auditable nonconformance workflow. The implementation links Supplier qualification, PO receipt lots, inspections, NCRs, disposition decisions and physical inventory movements.

## Supplier qualification

`supplier_quality_profiles` stores one profile per supplier:

- `APPROVED`
- `CONDITIONAL`
- `BLOCKED`
- `inspection_required`
- target PPM
- audit actor/time

Existing suppliers are migrated as `APPROVED` with `inspection_required=false` so upgrades do not unexpectedly stop production.

A `BLOCKED` supplier cannot be used for a new PO and cannot post a PO receipt. The rule exists in both Go services and PostgreSQL triggers. Open PO remainder from a blocked supplier is also excluded from MRP and ATP scheduled supply until the supplier is re-qualified.

When `inspection_required=true`, every new PO receipt lot is automatically put on `HOLD`. The lot cannot be consumed by normal ISSUE/FIFO processing until a PASS inspection releases it.

## NCR creation

A supplier-derived `FAIL` quality inspection automatically opens one NCR for that inspection. NCRs can also be opened manually by operator/planner/admin.

NCR evidence captures:

- supplier
- PO / purchase receipt
- item and lot
- source inspection
- affected quantity
- severity (`MINOR`, `MAJOR`, `CRITICAL`)
- description
- creator and timestamp

NCR evidence fields are immutable. Status transitions and disposition evidence are recorded in append-only history.

## Dispositions

Supported dispositions:

### RETURN_TO_SUPPLIER

Reduces the exact NCR lot using the Unified Inventory/Lot Ledger. The inventory transaction is a negative `ADJUST`, and the lot allocation is tagged `RETURN_TO_SUPPLIER`. Migration 0027 also overrides the unified-ledger immediate/deferred validators so only negative NCR `RETURN_TO_SUPPLIER` / `SCRAP` movements are accepted under an ADJUST header; arbitrary custom movement types remain rejected.

### SCRAP

Reduces the exact NCR lot using the Unified Inventory/Lot Ledger with movement type `SCRAP`.

### REWORK

No physical inventory movement occurs. The lot remains quarantined (`HOLD`) and the NCR enters `IN_REWORK`. The NCR can close only after a later PASS inspection.

### USE_AS_IS

Admin-only concession. No physical stock movement occurs. The lot can return to `OK` only when no other active NCR remains on the lot.

Planner/admin can disposition NCRs. `USE_AS_IS` additionally requires `admin` in both Go and PostgreSQL.

## Multiple NCR safety

A PASS inspection does not release a lot while another `OPEN` NCR remains. This prevents one inspection or concession from accidentally clearing an unrelated active nonconformance.

## Quality audit integration

`quality_status_history.inspection_id` is now nullable so supplier-quality state transitions can use the same lot-quality timeline. Supplier events write `source` / `source_ref`, including:

- `SUPPLIER_RECEIPT_HOLD`
- `NCR_OPEN`
- `NCR_DISPOSITION`

Inspection evidence remains immutable and inspection-linked rows retain the partial unique constraint on `inspection_id`.

## Supplier Quality Scorecard

`v_supplier_quality_scorecard` provides all-time supplier metrics:

- receipt count / received quantity
- inspection count
- fail inspections
- rejected lots
- defect quantity
- NCR count / open NCR / critical NCR
- return quantity
- scrap quantity
- defect PPM
- target PPM
- qualification status

API: `GET /api/supplier-quality/scorecard`

## API

- `GET /api/supplier-quality/suppliers`
- `POST /api/supplier-quality/suppliers`
- `GET /api/supplier-quality/scorecard`
- `GET /api/supplier-quality/ncrs`
- `POST /api/supplier-quality/ncrs`
- `GET /api/supplier-quality/ncrs/{id}/history`
- `POST /api/supplier-quality/ncrs/{id}/disposition`
- `POST /api/supplier-quality/ncrs/{id}/close-rework`

## RBAC

- viewer: read only
- operator: quality inspection + NCR creation
- planner: Supplier Quality profile management + NCR creation/disposition
- admin: all Supplier Quality functions, including `USE_AS_IS`

## Transaction boundaries

NCR disposition is atomic:

```text
NCR row lock
  -> validate actor/status
  -> exact-lot inventory movement (RETURN/SCRAP only)
  -> immutable disposition evidence
  -> NCR status transition
  -> lot quality transition / audit if needed
  -> COMMIT
```

Any failure rolls the entire operation back.
