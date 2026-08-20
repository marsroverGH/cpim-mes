-- ============================================================
-- 0012: S&OP (Sales & Operations Planning) と RCCP
-- ============================================================
--
-- S&OP は「品目グループ × 月次」レベルで需給バランスを調整する戦略レイヤー。
-- MPS (品目 × 週次) の上位概念。

-- ----- 品目グループ (Family) -----
CREATE TABLE IF NOT EXISTS item_groups (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code        text UNIQUE NOT NULL,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE items
  ADD COLUMN IF NOT EXISTS group_id uuid REFERENCES item_groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS items_group_idx ON items(group_id);

-- ----- S&OP 計画 (Family × Month) -----
CREATE TABLE IF NOT EXISTS sop_plans (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id      uuid NOT NULL REFERENCES item_groups(id) ON DELETE CASCADE,
  plan_month    date NOT NULL,    -- 月初日 (例: 2026-04-01)
  demand_qty    numeric NOT NULL DEFAULT 0,
  supply_qty    numeric NOT NULL DEFAULT 0,
  inventory_target numeric NOT NULL DEFAULT 0,
  notes         text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (group_id, plan_month)
);

CREATE INDEX IF NOT EXISTS sop_plans_month_idx ON sop_plans(plan_month);

-- ----- RCCP リソースプロファイル -----
-- 品目1単位を作るのに、各キーリソース (作業区) が消費する分数。
-- MPS だけ見て大まかに能力検証するための「単位負荷」係数。
CREATE TABLE IF NOT EXISTS rccp_profiles (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id         uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  work_center_id  uuid NOT NULL REFERENCES work_centers(id) ON DELETE CASCADE,
  minutes_per_unit numeric NOT NULL DEFAULT 0 CHECK (minutes_per_unit >= 0),
  UNIQUE (item_id, work_center_id)
);

-- ----- 既存品目を seed グループに振り分け -----
INSERT INTO item_groups (code, name) VALUES
  ('FAMILY-BIKE', '完成車ファミリー'),
  ('FAMILY-PARTS', '部品ファミリー')
ON CONFLICT (code) DO NOTHING;

UPDATE items SET group_id = (SELECT id FROM item_groups WHERE code='FAMILY-BIKE')
 WHERE type='FG' AND group_id IS NULL;
UPDATE items SET group_id = (SELECT id FROM item_groups WHERE code='FAMILY-PARTS')
 WHERE type IN ('SA','RM','PP') AND group_id IS NULL;

-- 既存ルーティングからRCCPプロファイルを近似初期化 (ロールアップ)
INSERT INTO rccp_profiles (item_id, work_center_id, minutes_per_unit)
SELECT r.item_id, ro.work_center_id,
       SUM(ro.run_minutes_per_unit) AS minutes_per_unit
  FROM routing_operations ro
  JOIN routings r ON r.id = ro.routing_id
 GROUP BY r.item_id, ro.work_center_id
ON CONFLICT (item_id, work_center_id) DO NOTHING;
