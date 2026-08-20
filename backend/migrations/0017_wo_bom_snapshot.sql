-- ============================================================
-- 0017: Immutable BOM snapshot per released Work Order
-- ============================================================
-- New releases capture the direct BOM inside the same transaction that creates
-- reservations and changes PLANNED -> RELEASED. Completion never reads the live
-- bom_components table; it consumes against these snapshot lines instead.
--
-- Existing RELEASED / IN_PROGRESS / COMPLETED / CLOSED WOs are backfilled as safely as possible:
--   * if RESERVE history exists, reconstruct the original effective quantity
--     per parent from the release reservation (best preservation of release state)
--   * otherwise, fall back to the currently committed direct BOM and mark the
--     snapshot source so the audit trail is explicit about the limitation.

CREATE TABLE IF NOT EXISTS work_order_bom_snapshots (
  id             uuid PRIMARY KEY,
  work_order_id  uuid NOT NULL UNIQUE REFERENCES work_orders(id) ON DELETE CASCADE,
  parent_item_id uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  captured_at    timestamptz NOT NULL DEFAULT now(),
  source         text NOT NULL DEFAULT 'RELEASE',
  notes          text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS wo_bom_snapshots_parent_idx
  ON work_order_bom_snapshots(parent_item_id, captured_at);

CREATE TABLE IF NOT EXISTS work_order_bom_snapshot_lines (
  id                      uuid PRIMARY KEY,
  snapshot_id             uuid NOT NULL REFERENCES work_order_bom_snapshots(id) ON DELETE CASCADE,
  line_no                 integer NOT NULL CHECK (line_no > 0),
  source_bom_component_id uuid,
  child_item_id           uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  child_code              text NOT NULL,
  child_name              text NOT NULL,
  child_uom               text NOT NULL,
  quantity_per            numeric NOT NULL CHECK (quantity_per > 0),
  scrap_pct               numeric NOT NULL DEFAULT 0 CHECK (scrap_pct >= 0),
  required_qty            numeric NOT NULL CHECK (required_qty > 0),
  standard_cost_snapshot  numeric NOT NULL DEFAULT 0 CHECK (standard_cost_snapshot >= 0),
  UNIQUE (snapshot_id, line_no),
  UNIQUE (snapshot_id, child_item_id)
);

CREATE INDEX IF NOT EXISTS wo_bom_snapshot_lines_child_idx
  ON work_order_bom_snapshot_lines(child_item_id);

-- --------------------------------------------------------------------------
-- Legacy non-PLANNED WO backfill.  The snapshot header is created once and never
-- rewritten.  For WOs with a historical RESERVE transaction, the reserved
-- quantity represents the release-time effective requirement (including scrap),
-- so it is a better reconstruction source than today's live BOM.
-- --------------------------------------------------------------------------
INSERT INTO work_order_bom_snapshots
  (id, work_order_id, parent_item_id, captured_at, source, notes)
SELECT gen_random_uuid(), w.id, w.item_id, COALESCE(w.released_at, now()),
       CASE WHEN EXISTS (
              SELECT 1 FROM inventory_txns t
               WHERE t.ref_doc = 'WO:' || w.order_no
                 AND t.txn_type = 'RESERVE'
                 AND ABS(t.quantity) > 0
            )
            THEN 'LEGACY_RESERVATION_RECONSTRUCTED'
            ELSE 'LEGACY_CURRENT_BOM_FALLBACK'
       END,
       CASE WHEN EXISTS (
              SELECT 1 FROM inventory_txns t
               WHERE t.ref_doc = 'WO:' || w.order_no
                 AND t.txn_type = 'RESERVE'
                 AND ABS(t.quantity) > 0
            )
            THEN '0017 reconstructed effective quantity_per from original RESERVE history; original base qty/scrap split is not recoverable.'
            ELSE '0017 could not find release reservation history; snapshot copied from the BOM current at migration time.'
       END
  FROM work_orders w
 WHERE w.status IN ('RELEASED', 'IN_PROGRESS', 'COMPLETED', 'CLOSED')
ON CONFLICT (work_order_id) DO NOTHING;

-- Prefer original release RESERVE history where available.  Use total RESERVE,
-- not net reserved, because partial completions may already have UNRESERVE rows.
WITH reserve_totals AS (
  SELECT s.id AS snapshot_id,
         w.id AS work_order_id,
         w.quantity AS wo_quantity,
         t.item_id AS child_item_id,
         SUM(ABS(t.quantity)) AS original_required_qty
    FROM work_order_bom_snapshots s
    JOIN work_orders w ON w.id = s.work_order_id
    JOIN inventory_txns t
      ON t.ref_doc = 'WO:' || w.order_no
     AND t.txn_type = 'RESERVE'
   WHERE w.status IN ('RELEASED', 'IN_PROGRESS', 'COMPLETED', 'CLOSED')
     AND s.source = 'LEGACY_RESERVATION_RECONSTRUCTED'
   GROUP BY s.id, w.id, w.quantity, t.item_id
  HAVING SUM(ABS(t.quantity)) > 0
), numbered AS (
  SELECT r.*,
         ROW_NUMBER() OVER (PARTITION BY r.snapshot_id ORDER BY r.child_item_id)::integer AS line_no
    FROM reserve_totals r
)
INSERT INTO work_order_bom_snapshot_lines
  (id, snapshot_id, line_no, source_bom_component_id,
   child_item_id, child_code, child_name, child_uom,
   quantity_per, scrap_pct, required_qty, standard_cost_snapshot)
SELECT gen_random_uuid(), n.snapshot_id, n.line_no, NULL,
       n.child_item_id, i.code, i.name, i.uom,
       n.original_required_qty / NULLIF(n.wo_quantity, 0),
       0,
       n.original_required_qty,
       i.standard_cost
  FROM numbered n
  JOIN items i ON i.id = n.child_item_id
ON CONFLICT (snapshot_id, child_item_id) DO NOTHING;

-- If no reservation history exists, freeze the currently committed direct BOM.
WITH fallback_rows AS (
  SELECT s.id AS snapshot_id,
         w.quantity AS wo_quantity,
         b.id AS source_bom_component_id,
         b.child_id AS child_item_id,
         b.quantity AS quantity_per,
         b.scrap_pct,
         i.code, i.name, i.uom, i.standard_cost,
         ROW_NUMBER() OVER (PARTITION BY s.id ORDER BY b.child_id)::integer AS line_no
    FROM work_order_bom_snapshots s
    JOIN work_orders w ON w.id = s.work_order_id
    JOIN bom_components b ON b.parent_id = w.item_id
    JOIN items i ON i.id = b.child_id
   WHERE s.source = 'LEGACY_CURRENT_BOM_FALLBACK'
)
INSERT INTO work_order_bom_snapshot_lines
  (id, snapshot_id, line_no, source_bom_component_id,
   child_item_id, child_code, child_name, child_uom,
   quantity_per, scrap_pct, required_qty, standard_cost_snapshot)
SELECT gen_random_uuid(), f.snapshot_id, f.line_no, f.source_bom_component_id,
       f.child_item_id, f.code, f.name, f.uom,
       f.quantity_per, f.scrap_pct,
       f.wo_quantity * f.quantity_per * (1 + f.scrap_pct),
       f.standard_cost
  FROM fallback_rows f
ON CONFLICT (snapshot_id, child_item_id) DO NOTHING;

-- Link each physical completion event to the exact BOM snapshot used for it.
ALTER TABLE work_order_completions
  ADD COLUMN IF NOT EXISTS bom_snapshot_id uuid
    REFERENCES work_order_bom_snapshots(id) ON DELETE RESTRICT;

UPDATE work_order_completions c
   SET bom_snapshot_id = s.id
  FROM work_order_bom_snapshots s
 WHERE s.work_order_id = c.work_order_id
   AND c.bom_snapshot_id IS NULL;

CREATE INDEX IF NOT EXISTS wo_completions_bom_snapshot_idx
  ON work_order_completions(bom_snapshot_id);
