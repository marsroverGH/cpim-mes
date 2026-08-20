-- ============================================================
-- 0002: 工程能力 (CRP) 関連テーブル + 標準原価フィールド
-- ============================================================

CREATE TABLE IF NOT EXISTS work_centers (
  id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code                     text UNIQUE NOT NULL,
  name                     text NOT NULL,
  capacity_minutes_per_day numeric NOT NULL DEFAULT 480,
  efficiency               numeric NOT NULL DEFAULT 1.00,  -- 0–1
  utilization              numeric NOT NULL DEFAULT 0.85,  -- 0–1
  created_at               timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS routings (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id     uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  description text NOT NULL DEFAULT '',
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- 1品目につき有効ルーティングは1本に制約
CREATE UNIQUE INDEX IF NOT EXISTS routings_active_unique
  ON routings(item_id) WHERE is_active = true;

CREATE TABLE IF NOT EXISTS routing_operations (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  routing_id           uuid NOT NULL REFERENCES routings(id) ON DELETE CASCADE,
  seq_no               integer NOT NULL,
  work_center_id       uuid NOT NULL REFERENCES work_centers(id),
  description          text NOT NULL DEFAULT '',
  setup_minutes        numeric NOT NULL DEFAULT 0,
  run_minutes_per_unit numeric NOT NULL DEFAULT 0,
  UNIQUE (routing_id, seq_no)
);

CREATE INDEX IF NOT EXISTS routing_ops_wc_idx ON routing_operations(work_center_id);

-- ============================================================
-- 標準労務費レート (作業区別) - Cost Rollup で利用
-- ============================================================
ALTER TABLE work_centers ADD COLUMN IF NOT EXISTS labor_rate_per_minute numeric NOT NULL DEFAULT 50;

-- ============================================================
-- Seed data: 作業区とルーティング
-- ============================================================
INSERT INTO work_centers (code, name, capacity_minutes_per_day, efficiency, utilization, labor_rate_per_minute)
VALUES
  ('WC-ASSY',  '組立ライン',     480, 0.95, 0.85, 80),
  ('WC-WELD',  '溶接',            480, 0.90, 0.80, 100),
  ('WC-PAINT', '塗装ブース',      480, 0.85, 0.75, 60),
  ('WC-PACK',  '梱包',            480, 0.95, 0.90, 40)
ON CONFLICT (code) DO NOTHING;

DO $$
DECLARE
  bike uuid; frame uuid; wheel uuid;
  wc_assy uuid; wc_weld uuid; wc_paint uuid; wc_pack uuid;
  rt_bike uuid; rt_frame uuid; rt_wheel uuid;
BEGIN
  SELECT id INTO bike  FROM items WHERE code='BIKE-100';
  SELECT id INTO frame FROM items WHERE code='FRAME-1';
  SELECT id INTO wheel FROM items WHERE code='WHEEL-1';

  SELECT id INTO wc_assy  FROM work_centers WHERE code='WC-ASSY';
  SELECT id INTO wc_weld  FROM work_centers WHERE code='WC-WELD';
  SELECT id INTO wc_paint FROM work_centers WHERE code='WC-PAINT';
  SELECT id INTO wc_pack  FROM work_centers WHERE code='WC-PACK';

  -- BIKE-100 ルーティング: 組立 → 塗装 → 梱包
  IF bike IS NOT NULL AND NOT EXISTS (SELECT 1 FROM routings WHERE item_id=bike AND is_active) THEN
    INSERT INTO routings(item_id, description, is_active) VALUES (bike, '完成車組立', true)
      RETURNING id INTO rt_bike;
    INSERT INTO routing_operations(routing_id, seq_no, work_center_id, description, setup_minutes, run_minutes_per_unit) VALUES
      (rt_bike, 10, wc_assy,  '組立',     30, 12),
      (rt_bike, 20, wc_paint, '塗装乾燥', 15,  8),
      (rt_bike, 30, wc_pack,  '梱包',     10,  3);
  END IF;

  -- FRAME-1 ルーティング: 溶接 → 塗装
  IF frame IS NOT NULL AND NOT EXISTS (SELECT 1 FROM routings WHERE item_id=frame AND is_active) THEN
    INSERT INTO routings(item_id, description, is_active) VALUES (frame, 'フレーム製造', true)
      RETURNING id INTO rt_frame;
    INSERT INTO routing_operations(routing_id, seq_no, work_center_id, description, setup_minutes, run_minutes_per_unit) VALUES
      (rt_frame, 10, wc_weld,  '溶接', 20, 18),
      (rt_frame, 20, wc_paint, '下塗', 10,  5);
  END IF;

  -- WHEEL-1 ルーティング: 組立のみ
  IF wheel IS NOT NULL AND NOT EXISTS (SELECT 1 FROM routings WHERE item_id=wheel AND is_active) THEN
    INSERT INTO routings(item_id, description, is_active) VALUES (wheel, 'ホイール組立', true)
      RETURNING id INTO rt_wheel;
    INSERT INTO routing_operations(routing_id, seq_no, work_center_id, description, setup_minutes, run_minutes_per_unit) VALUES
      (rt_wheel, 10, wc_assy, 'ホイール組立', 5, 4);
  END IF;
END$$;
