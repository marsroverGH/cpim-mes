-- ============================================================================
-- 0028: CPIM-style ABC analysis performance support
--
-- ABC classification is now based on rolling 12-month annual dollar usage:
--   SUM(abs(ISSUE qty)) × current standard_cost
--
-- Only physical ISSUE transactions are usage. ADJUST (cycle-count corrections,
-- NCR scrap/returns), RECEIPT and reservation events are intentionally excluded.
-- This partial index makes the rolling usage scan efficient while preserving the
-- canonical inventory_txns ledger as the source of truth.
-- ============================================================================

CREATE INDEX IF NOT EXISTS idx_inventory_txns_abc_issue_period
  ON inventory_txns (occurred_at, item_id)
  WHERE txn_type = 'ISSUE';

COMMENT ON INDEX idx_inventory_txns_abc_issue_period IS
  'Supports rolling 12-month ABC annual dollar usage from physical ISSUE transactions only';
