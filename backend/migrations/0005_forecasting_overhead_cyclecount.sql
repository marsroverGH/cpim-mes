-- ============================================================
-- 0005: 間接費配賦 + サイクルカウント
-- ============================================================

-- ----- 作業区に間接費レートを追加 -----
ALTER TABLE work_centers
  ADD COLUMN IF NOT EXISTS overhead_rate_per_minute numeric NOT NULL DEFAULT 30;

-- 既存サンプル作業区の間接費レートを設定
UPDATE work_centers SET overhead_rate_per_minute = 40 WHERE code = 'WC-ASSY';
UPDATE work_centers SET overhead_rate_per_minute = 60 WHERE code = 'WC-WELD';
UPDATE work_centers SET overhead_rate_per_minute = 80 WHERE code = 'WC-PAINT';
UPDATE work_centers SET overhead_rate_per_minute = 25 WHERE code = 'WC-PACK';

-- ----- 売上/出荷履歴 (需要予測の入力データ) -----
-- 既存の demand_forecasts (source='ORDER') を実績データとみなす設計のため
-- 新テーブル不要。ただし高速化のため部分インデックスを追加。
CREATE INDEX IF NOT EXISTS demand_orders_idx
  ON demand_forecasts(item_id, due_date)
  WHERE source = 'ORDER';

-- ----- サイクルカウント計画 -----
CREATE TABLE IF NOT EXISTS cycle_counts (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id     uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  abc_class   text NOT NULL CHECK (abc_class IN ('A','B','C')),
  scheduled_date date NOT NULL,
  counted_date   date,
  expected_qty   numeric,
  counted_qty    numeric,
  variance       numeric GENERATED ALWAYS AS (counted_qty - expected_qty) STORED,
  status         text NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING','COUNTED','RECONCILED')),
  notes          text NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cycle_counts_status_idx ON cycle_counts(status, scheduled_date);
CREATE INDEX IF NOT EXISTS cycle_counts_item_idx   ON cycle_counts(item_id);

-- ----- サンプルの実績データ (過去90日分の出荷履歴) -----
DO $$
DECLARE
  bike uuid;
  d int;
BEGIN
  SELECT id INTO bike FROM items WHERE code = 'BIKE-100';
  IF bike IS NULL THEN RETURN; END IF;

  -- 既に historical orders がある場合はスキップ
  IF EXISTS (SELECT 1 FROM demand_forecasts WHERE item_id = bike AND source = 'ORDER') THEN
    RETURN;
  END IF;

  -- 過去90日, 7日おきに出荷実績を生成 (週次パターン + 軽いトレンド)
  FOR d IN 0..12 LOOP
    INSERT INTO demand_forecasts(item_id, due_date, quantity, source)
    VALUES (
      bike,
      current_date - (d * 7),
      40 + (d * 2) + (((d * 17) % 13) - 6),  -- 軽いトレンド + ノイズ
      'ORDER'
    );
  END LOOP;
END$$;
