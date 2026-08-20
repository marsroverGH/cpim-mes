-- ============================================================
-- 0016: Atomic WO release / duplicate-reservation guard
-- ============================================================
-- ReleaseWorkOrder now serializes competing releases with:
--   1) SELECT ... FOR UPDATE on the work_orders row
--   2) SELECT ... FOR UPDATE on every direct component item row
--      in deterministic UUID order
--   3) stock re-check AFTER those locks are acquired
--   4) RESERVE inserts + RELEASED transition in one transaction
--
-- This partial unique index is defense-in-depth.  A work order may create at
-- most one RESERVE transaction for a given component.  UNRESERVE and other
-- inventory movements remain unrestricted.

DO $$
DECLARE
  duplicate_count integer;
BEGIN
  SELECT COUNT(*) INTO duplicate_count
    FROM (
      SELECT item_id, ref_doc
        FROM inventory_txns
       WHERE txn_type = 'RESERVE'
         AND ref_doc LIKE 'WO:%'
       GROUP BY item_id, ref_doc
      HAVING COUNT(*) > 1
    ) d;

  IF duplicate_count > 0 THEN
    RAISE EXCEPTION
      '0016 cannot create WO reservation uniqueness guard: % duplicate WO/component reservation group(s) already exist. Reconcile duplicate RESERVE transactions first.',
      duplicate_count;
  END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS ux_inventory_txns_wo_reserve_item
  ON inventory_txns (item_id, ref_doc)
  WHERE txn_type = 'RESERVE'
    AND ref_doc LIKE 'WO:%';
