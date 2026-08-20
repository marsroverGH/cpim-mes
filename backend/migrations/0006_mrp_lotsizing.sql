-- ============================================================
-- 0006: ロットサイジング方式 (LFL/FOQ/POQ/EOQ) と EOQ 計算用パラメータ
-- ============================================================

ALTER TABLE items
  ADD COLUMN IF NOT EXISTS lot_size_method text NOT NULL DEFAULT 'LFL'
    CHECK (lot_size_method IN ('LFL', 'FOQ', 'POQ', 'EOQ')),
  ADD COLUMN IF NOT EXISTS poq_periods integer NOT NULL DEFAULT 1
    CHECK (poq_periods >= 1),
  ADD COLUMN IF NOT EXISTS ordering_cost numeric NOT NULL DEFAULT 0
    CHECK (ordering_cost >= 0),
  ADD COLUMN IF NOT EXISTS holding_cost_pct numeric NOT NULL DEFAULT 0.20
    CHECK (holding_cost_pct >= 0);
-- holding_cost_pct: 標準原価に対する年間在庫保管費の比率 (例: 0.20 = 20%/年)
-- 年間需要は demand_forecasts(source='ORDER') から実績で算出。

-- Seed: サンプル品目に異なる方式を設定 (動作確認用)
UPDATE items SET lot_size_method = 'FOQ' WHERE code = 'BIKE-100';
UPDATE items SET lot_size_method = 'EOQ', ordering_cost = 5000 WHERE code = 'TIRE-1';
UPDATE items SET lot_size_method = 'POQ', poq_periods = 2 WHERE code = 'CHAIN-1';
