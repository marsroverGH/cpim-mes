-- ============================================================
-- 0007: 業務一貫フロー (WO予約 / PO入荷 / WO完成)
-- ============================================================

-- ----- inventory_txns に予約区分を追加 -----
ALTER TABLE inventory_txns
  DROP CONSTRAINT IF EXISTS inventory_txns_txn_type_check;

ALTER TABLE inventory_txns
  ADD CONSTRAINT inventory_txns_txn_type_check
  CHECK (txn_type IN ('RECEIPT', 'ISSUE', 'ADJUST', 'RESERVE', 'UNRESERVE'));

-- 注: RESERVE/UNRESERVE は数量が「物理在庫」を動かさず、予約残高だけを動かす論理操作。
-- 現在は SUM(quantity) で on-hand を出しているため、RESERVE/UNRESERVE の quantity は 0 にして
-- 別途 'reserved_qty' 列で管理する方針にすると整合的。本実装では区分のみ拡張し、
-- 予約残高は RESERVE/UNRESERVE の合計から算出する (on-hand とは別集計)。

-- ----- WO 完成時に生成された製造ロットへのリンク -----
ALTER TABLE work_orders
  ADD COLUMN IF NOT EXISTS produced_lot_id uuid REFERENCES lots(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS released_at  timestamptz,
  ADD COLUMN IF NOT EXISTS completed_at timestamptz;

-- ----- PO 入荷時に作られるロットへのリンク -----
ALTER TABLE purchase_orders
  ADD COLUMN IF NOT EXISTS received_lot_id uuid REFERENCES lots(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS received_at     timestamptz;

-- ----- 在庫サマリ用ビュー (物理 / 予約 / 利用可能) -----
-- on_hand     = SUM(qty WHERE type IN ('RECEIPT','ISSUE','ADJUST'))
-- reserved    = SUM(ABS(qty) WHERE type='RESERVE') - SUM(ABS(qty) WHERE type='UNRESERVE')
-- available   = on_hand - reserved
CREATE OR REPLACE VIEW v_stock_balance AS
SELECT
  i.id AS item_id,
  i.code,
  i.name,
  COALESCE(SUM(CASE WHEN t.txn_type IN ('RECEIPT','ISSUE','ADJUST') THEN t.quantity END), 0) AS on_hand,
  COALESCE(SUM(CASE
    WHEN t.txn_type = 'RESERVE'   THEN ABS(t.quantity)
    WHEN t.txn_type = 'UNRESERVE' THEN -ABS(t.quantity)
    ELSE 0 END), 0) AS reserved
FROM items i
LEFT JOIN inventory_txns t ON t.item_id = i.id
GROUP BY i.id, i.code, i.name;
