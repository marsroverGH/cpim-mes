-- ============================================================
-- 0029: CRP finite-capacity scheduling snapshots
-- ============================================================

ALTER TABLE work_centers
  ADD COLUMN IF NOT EXISTS shift_start_minute integer NOT NULL DEFAULT 480
    CHECK (shift_start_minute >= 0 AND shift_start_minute <= 1439);

CREATE TABLE IF NOT EXISTS crp_schedule_runs (
  id                    uuid PRIMARY KEY,
  start_date            date NOT NULL,
  end_date              date NOT NULL,
  horizon_days          integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
  mode                  text NOT NULL CHECK (mode IN ('FINITE_FORWARD')),
  status                text NOT NULL CHECK (status IN ('BUILDING','COMPLETE')),
  generated_at          timestamptz NOT NULL DEFAULT now(),
  generated_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
  generated_by          text NOT NULL DEFAULT '',
  CHECK (end_date >= start_date)
);

CREATE INDEX IF NOT EXISTS crp_schedule_runs_generated_idx
  ON crp_schedule_runs(generated_at DESC);

CREATE TABLE IF NOT EXISTS crp_schedule_orders (
  id                   uuid PRIMARY KEY,
  run_id               uuid NOT NULL REFERENCES crp_schedule_runs(id) ON DELETE CASCADE,
  source_type          text NOT NULL CHECK (source_type IN ('FIRM_WO','MRP_PLANNED')),
  source_ref           text NOT NULL,
  work_order_id        uuid REFERENCES work_orders(id) ON DELETE RESTRICT,
  item_id              uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  item_code            text NOT NULL,
  quantity             numeric NOT NULL CHECK (quantity >= 0),
  priority             integer NOT NULL,
  earliest_start       timestamptz NOT NULL,
  due_at               timestamptz NOT NULL,
  scheduled_start      timestamptz,
  scheduled_end        timestamptz,
  required_minutes     numeric NOT NULL CHECK (required_minutes >= 0),
  scheduled_minutes    numeric NOT NULL CHECK (scheduled_minutes >= 0),
  unscheduled_minutes  numeric NOT NULL CHECK (unscheduled_minutes >= 0),
  tardy_minutes        numeric NOT NULL CHECK (tardy_minutes >= 0),
  schedule_status      text NOT NULL CHECK (schedule_status IN ('ON_TIME','LATE','PARTIAL','UNSCHEDULED')),
  UNIQUE(run_id, source_type, source_ref),
  CHECK (scheduled_minutes + unscheduled_minutes <= required_minutes + 0.000001),
  CHECK ((scheduled_start IS NULL) = (scheduled_end IS NULL)),
  CHECK (scheduled_start IS NULL OR scheduled_end >= scheduled_start),
  CHECK (source_type <> 'FIRM_WO' OR work_order_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS crp_schedule_orders_run_idx
  ON crp_schedule_orders(run_id, priority, due_at);

CREATE TABLE IF NOT EXISTS crp_schedule_segments (
  id                   uuid PRIMARY KEY,
  run_id               uuid NOT NULL REFERENCES crp_schedule_runs(id) ON DELETE CASCADE,
  schedule_order_id    uuid NOT NULL REFERENCES crp_schedule_orders(id) ON DELETE CASCADE,
  source_type          text NOT NULL CHECK (source_type IN ('FIRM_WO','MRP_PLANNED')),
  source_ref           text NOT NULL,
  item_id              uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  item_code            text NOT NULL,
  operation_seq        integer NOT NULL,
  operation_desc       text NOT NULL DEFAULT '',
  work_center_id       uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  work_center_code     text NOT NULL,
  work_center_name     text NOT NULL,
  segment_no           integer NOT NULL CHECK (segment_no > 0),
  start_at             timestamptz NOT NULL,
  end_at               timestamptz NOT NULL,
  load_minutes         numeric NOT NULL CHECK (load_minutes > 0),
  clock_minutes        numeric NOT NULL CHECK (clock_minutes > 0),
  effective_load_rate  numeric NOT NULL CHECK (effective_load_rate > 0),
  firm                  boolean NOT NULL DEFAULT false,
  CHECK (end_at > start_at),
  UNIQUE(run_id, schedule_order_id, segment_no)
);

CREATE INDEX IF NOT EXISTS crp_schedule_segments_wc_time_idx
  ON crp_schedule_segments(run_id, work_center_id, start_at, end_at);

CREATE OR REPLACE FUNCTION assert_crp_finite_run(p_run_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  r crp_schedule_runs%ROWTYPE;
BEGIN
  SELECT * INTO r FROM crp_schedule_runs WHERE id=p_run_id;
  IF NOT FOUND THEN RETURN; END IF;

  IF EXISTS (
    SELECT 1
      FROM crp_schedule_segments a
      JOIN crp_schedule_segments b
        ON b.run_id=a.run_id
       AND b.work_center_id=a.work_center_id
       AND b.id>a.id
       AND tstzrange(a.start_at,a.end_at,'[)') && tstzrange(b.start_at,b.end_at,'[)')
     WHERE a.run_id=p_run_id
  ) THEN
    RAISE EXCEPTION 'finite CRP schedule contains overlapping segments on the same work center'
      USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1
      FROM crp_schedule_segments s
     WHERE s.run_id=p_run_id
       AND (s.start_at::date < r.start_date OR s.start_at::date > r.end_date OR s.end_at::date > r.end_date + 1)
  ) THEN
    RAISE EXCEPTION 'finite CRP segment falls outside schedule horizon'
      USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1
      FROM crp_schedule_orders o
      LEFT JOIN LATERAL (
        SELECT COALESCE(sum(s.load_minutes),0) AS scheduled
          FROM crp_schedule_segments s
         WHERE s.schedule_order_id=o.id
      ) x ON true
     WHERE o.run_id=p_run_id
       AND abs(x.scheduled - o.scheduled_minutes) > 0.000001
  ) THEN
    RAISE EXCEPTION 'finite CRP order scheduled_minutes does not match its segment total'
      USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION crp_schedule_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE rid uuid;
BEGIN
  rid := COALESCE(NEW.run_id, OLD.run_id);
  PERFORM assert_crp_finite_run(rid);
  RETURN COALESCE(NEW,OLD);
END$$;

DROP TRIGGER IF EXISTS trg_crp_schedule_segments_guard ON crp_schedule_segments;
CREATE CONSTRAINT TRIGGER trg_crp_schedule_segments_guard
AFTER INSERT OR UPDATE OR DELETE ON crp_schedule_segments
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION crp_schedule_guard();

DROP TRIGGER IF EXISTS trg_crp_schedule_orders_guard ON crp_schedule_orders;
CREATE CONSTRAINT TRIGGER trg_crp_schedule_orders_guard
AFTER INSERT OR UPDATE OR DELETE ON crp_schedule_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION crp_schedule_guard();

CREATE OR REPLACE FUNCTION protect_completed_crp_snapshot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE run_status text;
BEGIN
  SELECT status INTO run_status
    FROM crp_schedule_runs
   WHERE id=COALESCE(OLD.run_id, NEW.run_id);
  IF run_status='COMPLETE' THEN
    RAISE EXCEPTION 'completed finite CRP schedule snapshots are immutable'
      USING ERRCODE='23514';
  END IF;
  RETURN COALESCE(NEW,OLD);
END$$;

DROP TRIGGER IF EXISTS trg_crp_orders_immutable ON crp_schedule_orders;
CREATE TRIGGER trg_crp_orders_immutable
BEFORE UPDATE OR DELETE ON crp_schedule_orders
FOR EACH ROW EXECUTE FUNCTION protect_completed_crp_snapshot();

DROP TRIGGER IF EXISTS trg_crp_segments_immutable ON crp_schedule_segments;
CREATE TRIGGER trg_crp_segments_immutable
BEFORE UPDATE OR DELETE ON crp_schedule_segments
FOR EACH ROW EXECUTE FUNCTION protect_completed_crp_snapshot();

CREATE OR REPLACE FUNCTION validate_crp_run_completion()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.status='COMPLETE' AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'completed finite CRP schedule run is immutable'
      USING ERRCODE='23514';
  END IF;
  IF NEW.status='COMPLETE' AND OLD.status<>'COMPLETE' THEN
    PERFORM assert_crp_finite_run(NEW.id);
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_crp_run_completion ON crp_schedule_runs;
CREATE TRIGGER trg_crp_run_completion
BEFORE UPDATE ON crp_schedule_runs
FOR EACH ROW EXECUTE FUNCTION validate_crp_run_completion();
