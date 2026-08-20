-- ============================================================
-- 0013: Engineering Change Orders (ECO/ECN)
-- ============================================================
--
-- ECO は BOM 変更要求の管理。ステータス: DRAFT → APPROVED → APPLIED
-- 有効日 (effective_date) に達した APPROVED な ECO を APPLIED に遷移させると、
-- 紐づいた変更内容が現行 BOM に適用される。

CREATE TABLE IF NOT EXISTS engineering_changes (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  eco_no          text UNIQUE NOT NULL,
  title           text NOT NULL,
  description     text NOT NULL DEFAULT '',
  status          text NOT NULL DEFAULT 'DRAFT'
    CHECK (status IN ('DRAFT', 'APPROVED', 'APPLIED', 'CANCELLED')),
  effective_date  date NOT NULL,
  requested_by    text NOT NULL DEFAULT '',
  approved_by     text NOT NULL DEFAULT '',
  applied_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS eco_status_idx ON engineering_changes(status, effective_date);

-- BOM 変更1行: ADD = 新規部品追加, REMOVE = 削除, MODIFY = 数量変更
CREATE TABLE IF NOT EXISTS eco_components (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  eco_id          uuid NOT NULL REFERENCES engineering_changes(id) ON DELETE CASCADE,
  action          text NOT NULL CHECK (action IN ('ADD', 'REMOVE', 'MODIFY')),
  parent_id       uuid NOT NULL REFERENCES items(id),
  child_id        uuid NOT NULL REFERENCES items(id),
  -- ADD/MODIFY 時の新数量。REMOVE 時は無視される
  new_quantity    numeric NOT NULL DEFAULT 0 CHECK (new_quantity >= 0),
  new_scrap_pct   numeric NOT NULL DEFAULT 0 CHECK (new_scrap_pct >= 0 AND new_scrap_pct <= 1),
  notes           text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS eco_components_eco_idx ON eco_components(eco_id);
