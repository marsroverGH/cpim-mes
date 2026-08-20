-- ============================================================
-- 0009: WIP 進捗管理 / 品質検査 / ATP
-- ============================================================

-- ----- WO に完成済み数量 (WIP / 部分完成) -----
ALTER TABLE work_orders
  ADD COLUMN IF NOT EXISTS completed_qty numeric NOT NULL DEFAULT 0
    CHECK (completed_qty >= 0);

-- ----- ロットの品質ステータス -----
ALTER TABLE lots
  ADD COLUMN IF NOT EXISTS quality_status text NOT NULL DEFAULT 'OK'
    CHECK (quality_status IN ('OK', 'HOLD', 'REJECTED'));
-- OK:       正常 (デフォルト、消費可能)
-- HOLD:     検査保留 (FIFO 消費から除外)
-- REJECTED: 不適合 (FIFO 消費から除外、別途廃棄処理が必要)

CREATE INDEX IF NOT EXISTS lots_quality_idx ON lots(item_id, quality_status);

-- ----- 品質検査記録 -----
CREATE TABLE IF NOT EXISTS quality_inspections (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  lot_id        uuid NOT NULL REFERENCES lots(id) ON DELETE CASCADE,
  inspector     text NOT NULL DEFAULT '',
  inspected_at  timestamptz NOT NULL DEFAULT now(),
  result        text NOT NULL CHECK (result IN ('PASS', 'FAIL', 'HOLD')),
  -- PASS: 検査合格 → ロット quality_status='OK' (消費可)
  -- FAIL: 検査不合格 → ロット quality_status='REJECTED'
  -- HOLD: 検査保留   → ロット quality_status='HOLD'
  defect_qty    numeric NOT NULL DEFAULT 0 CHECK (defect_qty >= 0),
  notes         text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS quality_inspections_lot_idx
  ON quality_inspections(lot_id, inspected_at DESC);
