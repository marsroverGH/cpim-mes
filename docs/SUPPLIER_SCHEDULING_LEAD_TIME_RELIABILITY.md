# 0035 Supplier Scheduling + Lead-Time Reliability

0035 makes purchasing dates evidence-driven. A Purchase Order keeps its original business due date, while supplier commitments, ASN information, and historical receipt performance form a separate auditable planning layer consumed by MRP, CTP, and Full Pegging.

## 1. Supplier schedule evidence

`SupplierScheduleEvent` is append-only. Supported events are:

- `CONFIRM`: supplier confirms quantity and delivery date.
- `REVISE`: supplier revises the current commitment.
- `ASN`: shipment notice with expected arrival date.
- `CANCEL`: invalidates prior active supplier schedule evidence without deleting history.

Events are serialized by Purchase Order and record the authenticated planner/admin actor. Each mutation carries a client event UUID for idempotent retry. In 0035 a supplier schedule date represents availability of the PO's entire current remaining quantity, so confirmation/ASN quantity must equal that remaining quantity; physical partial receipts remain supported independently. Received or closed Purchase Orders cannot receive new schedule evidence.

## 2. Canonical Purchase Order planning date

`v_purchase_order_planning_schedule` resolves one planning date for every PO. Evidence precedence is:

1. ASN expected arrival date.
2. Active supplier confirmation/revision date.
3. Reliability-adjusted date when the latest reliability run has enough samples.
4. Original PO due date.

Reliability is conservative: it can delay the planning date but cannot move a PO earlier than its original due date.

## 3. Lead-time reliability

A reliability refresh creates immutable `supplier_lead_time_runs` and `supplier_lead_time_results` evidence. Only fully received POs within the requested receipt window are samples. Results are calculated both for supplier+item and supplier-wide fallback profiles.

Metrics include sample count, average lead time, population standard deviation, P50, P90, on-time rate, average lateness, recommended lead time, and confidence.

Recommended lead time is:

`ceil(max(P90 lead time, average lead time + population standard deviation))`

A canonical SHA-256 hash freezes the result set for auditability.

## 4. MRP and CTP integration

Open PO supply uses the canonical PO planning date. For purchased/raw items without a firm PO, MRP and CTP use an effective reliability lead time. Because supplier selection is not yet a planning decision in this system, the effective item lead time is deliberately conservative: it uses the maximum sufficiently sampled recommendation among non-blocked suppliers and never reduces the item-master lead time.

MRP results expose both `planningLeadTimeDays` and `leadTimeSource`.

## 5. Full Pegging / Exception integration

0034 Full Pegging is extended with:

- Nodes: `SUPPLIER_CONFIRMATION`, `SUPPLIER_ASN`, `LEAD_TIME_PROFILE`.
- Edges: `CONFIRMED_BY`, `SHIPPED_BY`, `PLANNED_USING`.
- Exceptions: `SUPPLIER_CONFIRMATION_LATE`, `SUPPLIER_RELIABILITY_RISK`.

`LATE_PURCHASE_ORDER` is evaluated against the canonical expected delivery date rather than the original PO due date, so Root Cause reflects the latest supplier evidence.

## 6. Permissions

Planner/admin can create supplier schedule events and refresh reliability. Authenticated users may read supplier schedule and reliability evidence. DB triggers also validate mutation actors as defense in depth.

## 7. UI

Purchase Orders expose a Supplier Schedule dialog for confirmation, revision, ASN, cancellation, and history. `/supplier-scheduling` provides the current reliability profiles, planning-date provenance for open POs, and reliability run history.
