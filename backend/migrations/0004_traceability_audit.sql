-- ============================================================
-- 0004: ロット/シリアル追跡 + 監査ログ
-- ============================================================

-- ----- ロット (受入単位の物理ロット番号) -----
CREATE TABLE IF NOT EXISTS lots (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id       uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  lot_no        text NOT NULL,
  quantity      numeric NOT NULL CHECK (quantity > 0),
  received_at   timestamptz NOT NULL DEFAULT now(),
  expiry_date   date,
  supplier      text NOT NULL DEFAULT '',  -- 購買由来の場合
  source_doc    text NOT NULL DEFAULT '',  -- PO番号 or WO番号
  notes         text NOT NULL DEFAULT '',
  UNIQUE (item_id, lot_no)
);

CREATE INDEX IF NOT EXISTS lots_item_idx ON lots(item_id);

-- ----- ロット移動 (どの取引でどのロットがどれだけ動いたか) -----
CREATE TABLE IF NOT EXISTS lot_movements (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  lot_id       uuid NOT NULL REFERENCES lots(id) ON DELETE CASCADE,
  txn_id       uuid REFERENCES inventory_txns(id) ON DELETE SET NULL,
  quantity     numeric NOT NULL,    -- 正: 受入  / 負: 払出
  movement_type text NOT NULL CHECK (movement_type IN ('RECEIPT','ISSUE','ADJUST','CONSUMED','PRODUCED')),
  ref_doc      text NOT NULL DEFAULT '',  -- WO番号など
  occurred_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS lot_mv_lot_idx ON lot_movements(lot_id);
CREATE INDEX IF NOT EXISTS lot_mv_ref_idx ON lot_movements(ref_doc);

-- ----- 監査ログ -----
CREATE TABLE IF NOT EXISTS audit_log (
  id          bigserial PRIMARY KEY,
  username    text NOT NULL,
  user_role   text NOT NULL DEFAULT '',
  action      text NOT NULL,        -- e.g. "POST /api/items"
  resource    text NOT NULL,        -- e.g. "items", "work-orders"
  resource_id text NOT NULL DEFAULT '',
  http_status integer NOT NULL,
  ip_address  text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL DEFAULT now(),
  payload     jsonb                  -- diff or request body for forensics
);

CREATE INDEX IF NOT EXISTS audit_user_idx     ON audit_log(username);
CREATE INDEX IF NOT EXISTS audit_resource_idx ON audit_log(resource);
CREATE INDEX IF NOT EXISTS audit_time_idx     ON audit_log(occurred_at DESC);

-- ----- Seed: 既存初期在庫に対して仮想ロット番号を付与 -----
DO $$
DECLARE
  rec record;
  lot_uuid uuid;
BEGIN
  -- inventory_txns の INIT 受入に対し、対応するロットを作成
  FOR rec IN SELECT t.id AS txn_id, t.item_id, t.quantity
               FROM inventory_txns t
              WHERE t.txn_type = 'RECEIPT' AND t.ref_doc = 'INIT'
                AND NOT EXISTS (SELECT 1 FROM lot_movements lm WHERE lm.txn_id = t.id)
  LOOP
    INSERT INTO lots(item_id, lot_no, quantity, source_doc, supplier, notes)
    VALUES (rec.item_id, 'INIT-' || substring(rec.txn_id::text, 1, 8),
            rec.quantity, 'INIT', 'INITIAL-STOCK', 'システム初期在庫')
    ON CONFLICT (item_id, lot_no) DO NOTHING
    RETURNING id INTO lot_uuid;

    IF lot_uuid IS NOT NULL THEN
      INSERT INTO lot_movements(lot_id, txn_id, quantity, movement_type, ref_doc)
      VALUES (lot_uuid, rec.txn_id, rec.quantity, 'RECEIPT', 'INIT');
    END IF;
  END LOOP;
END$$;
