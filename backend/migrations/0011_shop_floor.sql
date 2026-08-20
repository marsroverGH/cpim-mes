-- ============================================================
-- 0011: Shop Floor Control (工程実績記録)
-- ============================================================

-- WO リリース時に、ルーティング工程をコピーして wo_operations に展開する。
-- 各工程は独立にステータス遷移 (PENDING → IN_PROGRESS → COMPLETED) する。
CREATE TABLE IF NOT EXISTS wo_operations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wo_id           uuid NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
  seq_no          int  NOT NULL,
  work_center_id  uuid NOT NULL REFERENCES work_centers(id),
  description     text NOT NULL DEFAULT '',
  -- 計画値 (ルーティングからコピー)
  planned_setup_min   numeric NOT NULL DEFAULT 0 CHECK (planned_setup_min >= 0),
  planned_run_per_unit numeric NOT NULL DEFAULT 0 CHECK (planned_run_per_unit >= 0),
  -- 実績値
  actual_minutes  numeric NOT NULL DEFAULT 0 CHECK (actual_minutes >= 0),
  completed_qty   numeric NOT NULL DEFAULT 0 CHECK (completed_qty >= 0),
  status          text NOT NULL DEFAULT 'PENDING'
    CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED')),
  operator        text NOT NULL DEFAULT '',
  started_at      timestamptz,
  completed_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (wo_id, seq_no)
);

CREATE INDEX IF NOT EXISTS wo_operations_wo_idx ON wo_operations(wo_id, seq_no);
CREATE INDEX IF NOT EXISTS wo_operations_status_idx ON wo_operations(status, work_center_id);

-- 工程イベントログ (START / STOP / COMPLETE / SCRAP)
CREATE TABLE IF NOT EXISTS operation_logs (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wo_op_id      uuid NOT NULL REFERENCES wo_operations(id) ON DELETE CASCADE,
  event_type    text NOT NULL CHECK (event_type IN ('START', 'STOP', 'COMPLETE', 'SCRAP')),
  event_at      timestamptz NOT NULL DEFAULT now(),
  operator      text NOT NULL DEFAULT '',
  quantity      numeric NOT NULL DEFAULT 0,
  notes         text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS operation_logs_op_idx ON operation_logs(wo_op_id, event_at);
