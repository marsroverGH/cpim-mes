-- ============================================================
-- 0015: WO 部分完成 / 完成履歴 / 進捗報告分離
-- ============================================================
-- completed_qty は「在庫・材料消費まで会計済みの完成数量」に限定する。
-- reported_progress_qty は現場の参考進捗で、在庫を動かさない。

ALTER TABLE work_orders
  ADD COLUMN IF NOT EXISTS reported_progress_qty numeric NOT NULL DEFAULT 0
    CHECK (reported_progress_qty >= 0);

-- 旧版では completed_qty が単なる進捗入力にも使われていた。
-- RELEASED / IN_PROGRESS の値は在庫移動を伴っていないため、参考進捗へ退避し、
-- 会計済み完成数量は 0 から再開する。
UPDATE work_orders
   SET reported_progress_qty = GREATEST(reported_progress_qty, completed_qty),
       completed_qty = 0
 WHERE status IN ('RELEASED', 'IN_PROGRESS')
   AND completed_qty > 0;

CREATE TABLE IF NOT EXISTS work_order_completions (
  id              uuid PRIMARY KEY,
  work_order_id   uuid NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
  quantity        numeric NOT NULL CHECK (quantity > 0),
  produced_lot_id uuid NOT NULL REFERENCES lots(id) ON DELETE RESTRICT,
  completed_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS wo_completions_wo_idx
  ON work_order_completions(work_order_id, completed_at);

-- 旧版で既に完全完了済みのWOは、既存の完成ロットを履歴へ登録する。
INSERT INTO work_order_completions (id, work_order_id, quantity, produced_lot_id, completed_at)
SELECT gen_random_uuid(), w.id,
       CASE WHEN w.completed_qty > 0 THEN w.completed_qty ELSE w.quantity END,
       w.produced_lot_id,
       COALESCE(w.completed_at, now())
  FROM work_orders w
 WHERE w.status IN ('COMPLETED', 'CLOSED')
   AND w.produced_lot_id IS NOT NULL
   AND NOT EXISTS (
       SELECT 1 FROM work_order_completions c WHERE c.work_order_id = w.id
   );
