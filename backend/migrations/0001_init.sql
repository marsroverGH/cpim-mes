-- ============================================================
-- CPIM-MES initial schema
-- Loaded automatically by the postgres image at first start.
-- ============================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS items (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code            text UNIQUE NOT NULL,
  name            text NOT NULL,
  type            text NOT NULL CHECK (type IN ('FG','SA','RM','PP')),
  uom             text NOT NULL DEFAULT 'EA',
  lead_time_days  integer NOT NULL DEFAULT 0,
  safety_stock    numeric NOT NULL DEFAULT 0,
  lot_size        numeric NOT NULL DEFAULT 1,
  standard_cost   numeric NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS bom_components (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id   uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  child_id    uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  quantity    numeric NOT NULL CHECK (quantity > 0),
  scrap_pct   numeric NOT NULL DEFAULT 0,
  CONSTRAINT  bom_no_self_reference CHECK (parent_id <> child_id),
  UNIQUE (parent_id, child_id)
);

CREATE INDEX IF NOT EXISTS bom_parent_idx ON bom_components(parent_id);
CREATE INDEX IF NOT EXISTS bom_child_idx  ON bom_components(child_id);

CREATE TABLE IF NOT EXISTS demand_forecasts (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id     uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  due_date    date NOT NULL,
  quantity    numeric NOT NULL CHECK (quantity > 0),
  source      text NOT NULL DEFAULT 'FORECAST',
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mps_entries (
  id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id   uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  period    date NOT NULL,
  planned   numeric NOT NULL DEFAULT 0,
  released  numeric NOT NULL DEFAULT 0,
  UNIQUE (item_id, period)
);

CREATE TABLE IF NOT EXISTS inventory_txns (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id     uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  quantity    numeric NOT NULL,
  txn_type    text NOT NULL CHECK (txn_type IN ('RECEIPT','ISSUE','ADJUST')),
  ref_doc     text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS txn_item_idx ON inventory_txns(item_id, occurred_at);

CREATE TABLE IF NOT EXISTS work_orders (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_no    text UNIQUE NOT NULL,
  item_id     uuid NOT NULL REFERENCES items(id),
  quantity    numeric NOT NULL CHECK (quantity > 0),
  start_date  date NOT NULL,
  due_date    date NOT NULL,
  status      text NOT NULL DEFAULT 'PLANNED'
              CHECK (status IN ('PLANNED','RELEASED','IN_PROGRESS','COMPLETED','CLOSED')),
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS purchase_orders (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  po_no       text UNIQUE NOT NULL,
  item_id     uuid NOT NULL REFERENCES items(id),
  supplier    text NOT NULL,
  quantity    numeric NOT NULL CHECK (quantity > 0),
  order_date  date NOT NULL DEFAULT current_date,
  due_date    date NOT NULL,
  status      text NOT NULL DEFAULT 'OPEN'
              CHECK (status IN ('OPEN','RECEIVED','CLOSED'))
);

-- ============================================================
-- Seed data — 自転車1台のミニBOMで動作確認できるサンプル
-- ============================================================

INSERT INTO items (code, name, type, uom, lead_time_days, lot_size, standard_cost)
VALUES
  ('BIKE-100', '完成車 Standard',     'FG', 'EA', 5, 10, 35000),
  ('FRAME-1',  'フレーム Aluminium',  'SA', 'EA', 3, 20,  8000),
  ('WHEEL-1',  'ホイール 26"',        'SA', 'EA', 2, 50,  3500),
  ('TIRE-1',   'タイヤ 26x1.95',      'PP', 'EA', 7, 100, 1200),
  ('TUBE-1',   'チューブ 26"',        'PP', 'EA', 7, 100,  300),
  ('SADDLE-1', 'サドル Comfort',      'PP', 'EA', 4, 50,  1500),
  ('CHAIN-1',  'チェーン 1/2"',       'PP', 'EA', 5, 50,   800)
ON CONFLICT (code) DO NOTHING;

-- BOM: BIKE-100 = 1 FRAME + 2 WHEEL + 1 SADDLE + 1 CHAIN
--      WHEEL-1 = 1 TIRE + 1 TUBE
DO $$
DECLARE
  bike uuid; frame uuid; wheel uuid; tire uuid; tube uuid; saddle uuid; chain uuid;
BEGIN
  SELECT id INTO bike   FROM items WHERE code='BIKE-100';
  SELECT id INTO frame  FROM items WHERE code='FRAME-1';
  SELECT id INTO wheel  FROM items WHERE code='WHEEL-1';
  SELECT id INTO tire   FROM items WHERE code='TIRE-1';
  SELECT id INTO tube   FROM items WHERE code='TUBE-1';
  SELECT id INTO saddle FROM items WHERE code='SADDLE-1';
  SELECT id INTO chain  FROM items WHERE code='CHAIN-1';

  INSERT INTO bom_components(parent_id, child_id, quantity, scrap_pct) VALUES
    (bike, frame,  1, 0.00),
    (bike, wheel,  2, 0.01),
    (bike, saddle, 1, 0.00),
    (bike, chain,  1, 0.02),
    (wheel, tire,  1, 0.01),
    (wheel, tube,  1, 0.05)
  ON CONFLICT (parent_id, child_id) DO NOTHING;

  -- 初期在庫
  INSERT INTO inventory_txns(item_id, quantity, txn_type, ref_doc) VALUES
    (frame, 30, 'RECEIPT', 'INIT'),
    (wheel, 80, 'RECEIPT', 'INIT'),
    (tire, 200, 'RECEIPT', 'INIT'),
    (tube, 200, 'RECEIPT', 'INIT'),
    (saddle, 60, 'RECEIPT', 'INIT'),
    (chain, 100, 'RECEIPT', 'INIT');

  -- 需要予測
  INSERT INTO demand_forecasts(item_id, due_date, quantity, source) VALUES
    (bike, current_date + 14, 50,  'FORECAST'),
    (bike, current_date + 28, 75,  'FORECAST');
END$$;
