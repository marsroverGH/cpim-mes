-- ============================================================
-- 0014: MRP v2 - MPS-driven planning compatibility seed
-- ============================================================
-- The MRP engine now uses mps_entries as its independent-demand driver.
-- For existing/demo databases that only contain legacy FORECAST rows,
-- backfill matching MPS rows once without overwriting planner-entered MPS.

INSERT INTO mps_entries (item_id, period, planned, released)
SELECT d.item_id, d.due_date, SUM(d.quantity), 0
  FROM demand_forecasts d
 WHERE d.source = 'FORECAST'
 GROUP BY d.item_id, d.due_date
ON CONFLICT (item_id, period) DO NOTHING;
