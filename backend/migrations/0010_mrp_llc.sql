-- ============================================================
-- 0010: MRP 低レベルコード (Low-Level Code) 処理
-- ============================================================

-- 同一品目が BOM の複数階層に出現する場合、最深レベルで一括計算するために
-- 「低レベルコード (LLC)」を items に保持する。0 が最上位 (FG)、
-- 子の LLC は max(全親の LLC) + 1。

ALTER TABLE items
  ADD COLUMN IF NOT EXISTS low_level_code int NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS items_llc_idx ON items(low_level_code);

-- ----- LLC 再計算ストアド関数 -----
-- BOM 全件を走査し、トポロジカル順に最大深度を計算する。
-- 親→子のグラフで、子の LLC = max(親の LLC) + 1。
-- 循環参照は assert で検出 (CPIM/APICS の BOM では循環は禁止)。
CREATE OR REPLACE FUNCTION recompute_low_level_codes()
RETURNS void AS $$
DECLARE
  changed int;
  iter int := 0;
BEGIN
  -- 全品目の LLC を一旦 0 に
  UPDATE items SET low_level_code = 0;

  -- 親の LLC + 1 が子の LLC より大きければ更新、変化が無くなるまで反復
  LOOP
    UPDATE items c
       SET low_level_code = sub.new_llc
      FROM (
        SELECT b.child_id AS id, MAX(p.low_level_code) + 1 AS new_llc
          FROM bom_components b
          JOIN items p ON p.id = b.parent_id
         GROUP BY b.child_id
      ) sub
     WHERE c.id = sub.id
       AND sub.new_llc > c.low_level_code;
    GET DIAGNOSTICS changed = ROW_COUNT;
    iter := iter + 1;
    EXIT WHEN changed = 0;
    IF iter > 100 THEN
      RAISE EXCEPTION 'LLC recompute did not converge in 100 iterations (cyclic BOM?)';
    END IF;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- 初回計算
SELECT recompute_low_level_codes();
