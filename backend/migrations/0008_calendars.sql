-- ============================================================
-- 0008: 作業カレンダー (休日 / 稼働日例外)
-- ============================================================

-- ----- カレンダーヘッダ -----
CREATE TABLE IF NOT EXISTS work_calendars (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code         text UNIQUE NOT NULL,
  name         text NOT NULL,
  is_default   boolean NOT NULL DEFAULT false,
  -- 標準週次パターン: 月〜日それぞれの稼働分数 (0 なら休業)
  monday_min      integer NOT NULL DEFAULT 480 CHECK (monday_min    >= 0 AND monday_min    <= 1440),
  tuesday_min     integer NOT NULL DEFAULT 480 CHECK (tuesday_min   >= 0 AND tuesday_min   <= 1440),
  wednesday_min   integer NOT NULL DEFAULT 480 CHECK (wednesday_min >= 0 AND wednesday_min <= 1440),
  thursday_min    integer NOT NULL DEFAULT 480 CHECK (thursday_min  >= 0 AND thursday_min  <= 1440),
  friday_min      integer NOT NULL DEFAULT 480 CHECK (friday_min    >= 0 AND friday_min    <= 1440),
  saturday_min    integer NOT NULL DEFAULT 0   CHECK (saturday_min  >= 0 AND saturday_min  <= 1440),
  sunday_min      integer NOT NULL DEFAULT 0   CHECK (sunday_min    >= 0 AND sunday_min    <= 1440),
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- 1つだけ default にする部分一意インデックス
CREATE UNIQUE INDEX IF NOT EXISTS work_calendars_default_idx
  ON work_calendars(is_default) WHERE is_default;

-- ----- 個別日付の例外 (祝日 / 振替出勤など) -----
CREATE TABLE IF NOT EXISTS calendar_exceptions (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  calendar_id    uuid NOT NULL REFERENCES work_calendars(id) ON DELETE CASCADE,
  exception_date date NOT NULL,
  -- HOLIDAY: 休業 (minutes は 0 として扱う)
  -- WORKDAY: 標準では休みだが特例で稼働 (minutes 必須)
  kind           text NOT NULL CHECK (kind IN ('HOLIDAY','WORKDAY')),
  minutes        integer NOT NULL DEFAULT 0 CHECK (minutes >= 0 AND minutes <= 1440),
  description    text NOT NULL DEFAULT '',
  UNIQUE (calendar_id, exception_date)
);

CREATE INDEX IF NOT EXISTS calendar_exceptions_date_idx
  ON calendar_exceptions(calendar_id, exception_date);

-- ----- 作業区にカレンダー紐付け -----
ALTER TABLE work_centers
  ADD COLUMN IF NOT EXISTS calendar_id uuid REFERENCES work_calendars(id) ON DELETE SET NULL;

-- ----- 既定カレンダー (5日制 + 主要日本祝日 1件サンプル) -----
INSERT INTO work_calendars (code, name, is_default)
VALUES ('STD-5DAY', '標準カレンダー (月-金 8h)', true)
ON CONFLICT (code) DO NOTHING;

-- 既存の作業区を既定カレンダーに紐付け
UPDATE work_centers
   SET calendar_id = (SELECT id FROM work_calendars WHERE code = 'STD-5DAY')
 WHERE calendar_id IS NULL;

-- 当年の主要日本祝日サンプル (動作確認用、最低限)
DO $$
DECLARE
  cal_id uuid;
  yr int := EXTRACT(YEAR FROM current_date)::int;
BEGIN
  SELECT id INTO cal_id FROM work_calendars WHERE code = 'STD-5DAY';
  IF cal_id IS NULL THEN RETURN; END IF;

  INSERT INTO calendar_exceptions (calendar_id, exception_date, kind, description) VALUES
    (cal_id, make_date(yr, 1, 1),  'HOLIDAY', '元日'),
    (cal_id, make_date(yr, 5, 3),  'HOLIDAY', '憲法記念日'),
    (cal_id, make_date(yr, 5, 4),  'HOLIDAY', 'みどりの日'),
    (cal_id, make_date(yr, 5, 5),  'HOLIDAY', 'こどもの日'),
    (cal_id, make_date(yr, 8, 11), 'HOLIDAY', '山の日'),
    (cal_id, make_date(yr, 11, 3), 'HOLIDAY', '文化の日'),
    (cal_id, make_date(yr, 11, 23),'HOLIDAY', '勤労感謝の日')
  ON CONFLICT DO NOTHING;
END$$;
