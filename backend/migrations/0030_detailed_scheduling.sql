-- ============================================================
-- 0030: Detailed Scheduling
-- Alternative work centers, lot streaming / transfer batches,
-- sequence-dependent setup, machine-count and labor constraints.
-- ============================================================

BEGIN;

ALTER TABLE work_centers
  ADD COLUMN IF NOT EXISTS machine_count integer NOT NULL DEFAULT 1 CHECK (machine_count BETWEEN 1 AND 100),
  ADD COLUMN IF NOT EXISTS worker_count  integer NOT NULL DEFAULT 1 CHECK (worker_count BETWEEN 0 AND 1000);

ALTER TABLE routing_operations
  ADD COLUMN IF NOT EXISTS setup_family text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS overlap_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS transfer_batch_qty numeric NOT NULL DEFAULT 0 CHECK (transfer_batch_qty >= 0),
  ADD COLUMN IF NOT EXISTS machines_required integer NOT NULL DEFAULT 1 CHECK (machines_required > 0),
  ADD COLUMN IF NOT EXISTS workers_required integer NOT NULL DEFAULT 1 CHECK (workers_required >= 0);

DO $$ BEGIN
  ALTER TABLE routing_operations ADD CONSTRAINT routing_overlap_transfer_qty_chk
    CHECK (NOT overlap_enabled OR transfer_batch_qty > 0);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS routing_operation_alternatives (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  routing_operation_id  uuid NOT NULL REFERENCES routing_operations(id) ON DELETE CASCADE,
  work_center_id        uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  priority              integer NOT NULL DEFAULT 100 CHECK (priority >= 0),
  run_time_multiplier   numeric NOT NULL DEFAULT 1 CHECK (run_time_multiplier > 0),
  setup_time_multiplier numeric NOT NULL DEFAULT 1 CHECK (setup_time_multiplier > 0),
  is_active             boolean NOT NULL DEFAULT true,
  created_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE(routing_operation_id, work_center_id)
);
CREATE INDEX IF NOT EXISTS routing_operation_alt_op_idx
  ON routing_operation_alternatives(routing_operation_id, is_active, priority);

CREATE TABLE IF NOT EXISTS work_center_setup_matrix (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  work_center_id    uuid NOT NULL REFERENCES work_centers(id) ON DELETE CASCADE,
  from_setup_family text NOT NULL DEFAULT '*',
  to_setup_family   text NOT NULL,
  setup_minutes     numeric NOT NULL CHECK (setup_minutes >= 0),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE(work_center_id, from_setup_family, to_setup_family)
);
CREATE INDEX IF NOT EXISTS wc_setup_matrix_lookup_idx
  ON work_center_setup_matrix(work_center_id, to_setup_family, from_setup_family);

CREATE OR REPLACE FUNCTION enforce_detailed_routing_resource_master()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  primary_wc uuid;
BEGIN
  IF TG_TABLE_NAME='routing_operations' THEN
    IF NOT EXISTS (
      SELECT 1 FROM work_centers x
       WHERE x.id=NEW.work_center_id
         AND NEW.machines_required<=x.machine_count
         AND NEW.workers_required<=x.worker_count
    ) THEN
      RAISE EXCEPTION 'routing operation resource requirement exceeds primary work center capacity' USING ERRCODE='23514';
    END IF;
    IF EXISTS (
      SELECT 1
        FROM routing_operation_alternatives a
        JOIN work_centers x ON x.id=a.work_center_id
       WHERE a.routing_operation_id=NEW.id AND a.is_active
         AND (NEW.machines_required>x.machine_count OR NEW.workers_required>x.worker_count)
    ) THEN
      RAISE EXCEPTION 'routing operation resource requirement exceeds an active alternative work center capacity' USING ERRCODE='23514';
    END IF;
  ELSE
    SELECT ro.work_center_id INTO primary_wc
      FROM routing_operations ro WHERE ro.id=NEW.routing_operation_id;
    IF primary_wc IS NULL THEN
      RAISE EXCEPTION 'routing operation % not found', NEW.routing_operation_id USING ERRCODE='23503';
    END IF;
    IF NEW.work_center_id=primary_wc THEN
      RAISE EXCEPTION 'alternative work center must differ from primary work center' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS (
      SELECT 1
        FROM routing_operations ro
        JOIN work_centers x ON x.id=NEW.work_center_id
       WHERE ro.id=NEW.routing_operation_id
         AND ro.machines_required<=x.machine_count
         AND ro.workers_required<=x.worker_count
    ) THEN
      RAISE EXCEPTION 'routing alternative resource requirement exceeds alternative work center capacity' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_routing_operation_resource_master ON routing_operations;
CREATE TRIGGER trg_routing_operation_resource_master
BEFORE INSERT OR UPDATE OF work_center_id,machines_required,workers_required ON routing_operations
FOR EACH ROW EXECUTE FUNCTION enforce_detailed_routing_resource_master();
DROP TRIGGER IF EXISTS trg_routing_alternative_resource_master ON routing_operation_alternatives;
CREATE TRIGGER trg_routing_alternative_resource_master
BEFORE INSERT OR UPDATE ON routing_operation_alternatives
FOR EACH ROW EXECUTE FUNCTION enforce_detailed_routing_resource_master();

-- Do not allow a work-center resource reduction to invalidate an existing
-- primary or alternative routing requirement.
CREATE OR REPLACE FUNCTION enforce_work_center_resource_capacity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM routing_operations ro
     WHERE ro.work_center_id=NEW.id
       AND (ro.machines_required>NEW.machine_count OR ro.workers_required>NEW.worker_count)
  ) THEN
    RAISE EXCEPTION 'work center resource reduction would invalidate a primary routing operation' USING ERRCODE='23514';
  END IF;
  IF EXISTS (
    SELECT 1
      FROM routing_operation_alternatives a
      JOIN routing_operations ro ON ro.id=a.routing_operation_id
     WHERE a.work_center_id=NEW.id AND a.is_active
       AND (ro.machines_required>NEW.machine_count OR ro.workers_required>NEW.worker_count)
  ) THEN
    RAISE EXCEPTION 'work center resource reduction would invalidate an active routing alternative' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_work_center_resource_capacity ON work_centers;
CREATE TRIGGER trg_work_center_resource_capacity
BEFORE UPDATE OF machine_count,worker_count ON work_centers
FOR EACH ROW EXECUTE FUNCTION enforce_work_center_resource_capacity();

-- Freeze detailed-routing attributes at WO release so later master changes do
-- not alter the executable/scheduled manufacturing instruction.
ALTER TABLE wo_operations
  ADD COLUMN IF NOT EXISTS routing_operation_id uuid REFERENCES routing_operations(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS setup_family text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS overlap_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS transfer_batch_qty numeric NOT NULL DEFAULT 0 CHECK (transfer_batch_qty >= 0),
  ADD COLUMN IF NOT EXISTS machines_required integer NOT NULL DEFAULT 1 CHECK (machines_required > 0),
  ADD COLUMN IF NOT EXISTS workers_required integer NOT NULL DEFAULT 1 CHECK (workers_required >= 0);

DO $$ BEGIN
  ALTER TABLE wo_operations ADD CONSTRAINT wo_operation_overlap_transfer_qty_chk
    CHECK (NOT overlap_enabled OR transfer_batch_qty > 0);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Best-effort backfill for legacy released WOs from the current active routing.
UPDATE wo_operations woop
   SET routing_operation_id = ro.id,
       setup_family = ro.setup_family,
       overlap_enabled = ro.overlap_enabled,
       transfer_batch_qty = ro.transfer_batch_qty,
       machines_required = ro.machines_required,
       workers_required = ro.workers_required
  FROM work_orders w
  JOIN routings r ON r.item_id=w.item_id AND r.is_active=true
  JOIN routing_operations ro ON ro.routing_id=r.id
 WHERE woop.wo_id=w.id
   AND ro.seq_no=woop.seq_no
   AND woop.routing_operation_id IS NULL;

CREATE TABLE IF NOT EXISTS wo_operation_alternatives (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wo_operation_id       uuid NOT NULL REFERENCES wo_operations(id) ON DELETE CASCADE,
  work_center_id        uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  priority              integer NOT NULL DEFAULT 100 CHECK (priority >= 0),
  run_time_multiplier   numeric NOT NULL DEFAULT 1 CHECK (run_time_multiplier > 0),
  setup_time_multiplier numeric NOT NULL DEFAULT 1 CHECK (setup_time_multiplier > 0),
  source                text NOT NULL DEFAULT 'RELEASE_SNAPSHOT',
  UNIQUE(wo_operation_id, work_center_id)
);

INSERT INTO wo_operation_alternatives
  (id, wo_operation_id, work_center_id, priority, run_time_multiplier, setup_time_multiplier, source)
SELECT gen_random_uuid(), woop.id, a.work_center_id, a.priority,
       a.run_time_multiplier, a.setup_time_multiplier, 'LEGACY_CURRENT_ROUTING_FALLBACK'
  FROM wo_operations woop
  JOIN routing_operation_alternatives a ON a.routing_operation_id=woop.routing_operation_id AND a.is_active=true
ON CONFLICT (wo_operation_id, work_center_id) DO NOTHING;

-- Transfer-batch execution allows more than one routing step to be active once
-- the immediate predecessor has physically completed the required transfer qty.
DROP INDEX IF EXISTS wo_operations_one_active_step_uq;

CREATE OR REPLACE FUNCTION wo_predecessor_transfer_ready(p_wo_id uuid, p_seq_no integer)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT COALESCE((
    SELECT CASE
      WHEN p.status='COMPLETED' THEN true
      WHEN p.overlap_enabled AND p.transfer_batch_qty > 0
        THEN p.completed_qty + 0.000001 >= LEAST(p.transfer_batch_qty, w.quantity)
      ELSE false
    END
      FROM wo_operations p
      JOIN work_orders w ON w.id=p.wo_id
     WHERE p.wo_id=p_wo_id AND p.seq_no < p_seq_no
     ORDER BY p.seq_no DESC
     LIMIT 1
  ), true)
$$;

CREATE OR REPLACE FUNCTION enforce_wo_operation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  planned_qty numeric;
  db_username text;
  predecessor_completed numeric;
BEGIN
  SELECT quantity INTO planned_qty FROM work_orders WHERE id=NEW.wo_id;
  IF planned_qty IS NULL THEN
    RAISE EXCEPTION 'work order % not found for operation %', NEW.wo_id, NEW.id USING ERRCODE='23514';
  END IF;

  IF NEW.completed_qty < OLD.completed_qty THEN
    RAISE EXCEPTION 'operation cumulative completed quantity cannot decrease (% -> %)', OLD.completed_qty, NEW.completed_qty USING ERRCODE='23514';
  END IF;
  IF NEW.completed_qty > planned_qty THEN
    RAISE EXCEPTION 'operation completed quantity % exceeds WO planned quantity %', NEW.completed_qty, planned_qty USING ERRCODE='23514';
  END IF;

  IF NEW.status IS DISTINCT FROM OLD.status THEN
    IF NOT (
      (OLD.status='PENDING'     AND NEW.status='READY') OR
      (OLD.status='READY'       AND NEW.status='IN_PROGRESS') OR
      (OLD.status='IN_PROGRESS' AND NEW.status='PAUSED') OR
      (OLD.status='PAUSED'      AND NEW.status='IN_PROGRESS') OR
      (OLD.status='IN_PROGRESS' AND NEW.status='COMPLETED')
    ) THEN
      RAISE EXCEPTION 'invalid Shop Floor state transition: % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    END IF;
  END IF;

  IF NEW.status IN ('READY','IN_PROGRESS') AND NOT wo_predecessor_transfer_ready(NEW.wo_id, NEW.seq_no) THEN
    RAISE EXCEPTION 'operation % cannot become % before predecessor transfer quantity is available', NEW.seq_no, NEW.status USING ERRCODE='23514';
  END IF;

  -- A downstream operation may not report more good quantity than its immediate
  -- predecessor has physically produced, even when lot streaming is enabled.
  SELECT p.completed_qty INTO predecessor_completed
    FROM wo_operations p
   WHERE p.wo_id=NEW.wo_id AND p.seq_no<NEW.seq_no
   ORDER BY p.seq_no DESC LIMIT 1;
  IF predecessor_completed IS NOT NULL AND NEW.completed_qty > predecessor_completed + 0.000001 THEN
    RAISE EXCEPTION 'operation completed quantity % exceeds predecessor completed quantity %', NEW.completed_qty, predecessor_completed USING ERRCODE='23514';
  END IF;

  IF NEW.status='COMPLETED' THEN
    IF NEW.completed_qty < planned_qty THEN
      RAISE EXCEPTION 'operation cannot become COMPLETED before cumulative quantity reaches WO planned quantity' USING ERRCODE='23514';
    END IF;
    IF NEW.completed_at IS NULL THEN
      RAISE EXCEPTION 'COMPLETED operation must have completed_at' USING ERRCODE='23514';
    END IF;
  END IF;

  IF NEW.operator_user_id IS NOT NULL THEN
    SELECT username INTO db_username FROM users WHERE id=NEW.operator_user_id;
    IF db_username IS NULL OR db_username IS DISTINCT FROM NEW.operator THEN
      RAISE EXCEPTION 'operation operator identity does not match users table' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION assert_wo_operation_sequence(p_wo_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  wo_status text;
BEGIN
  SELECT status INTO wo_status FROM work_orders WHERE id=p_wo_id;
  IF wo_status IS NULL THEN RETURN; END IF;

  IF EXISTS (
    SELECT 1 FROM wo_operations o
     WHERE o.wo_id=p_wo_id
       AND o.status IN ('READY','IN_PROGRESS','PAUSED')
       AND NOT wo_predecessor_transfer_ready(o.wo_id,o.seq_no)
  ) THEN
    RAISE EXCEPTION 'WO % has an active/ready operation before predecessor transfer quantity is available', p_wo_id USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM wo_operations o
     JOIN LATERAL (
       SELECT p.completed_qty
         FROM wo_operations p
        WHERE p.wo_id=o.wo_id AND p.seq_no<o.seq_no
        ORDER BY p.seq_no DESC LIMIT 1
     ) pred ON true
     WHERE o.wo_id=p_wo_id
       AND o.completed_qty > pred.completed_qty + 0.000001
  ) THEN
    RAISE EXCEPTION 'WO % has downstream completed quantity exceeding predecessor quantity', p_wo_id USING ERRCODE='23514';
  END IF;

  IF wo_status IN ('RELEASED','IN_PROGRESS') AND NOT EXISTS (
    SELECT 1 FROM wo_operations o
     WHERE o.wo_id=p_wo_id
       AND o.status IN ('READY','IN_PROGRESS','PAUSED')
  ) AND EXISTS (
    SELECT 1 FROM wo_operations o
     WHERE o.wo_id=p_wo_id AND o.status<>'COMPLETED'
  ) THEN
    RAISE EXCEPTION 'WO % has unfinished operations but no executable/active operation', p_wo_id USING ERRCODE='23514';
  END IF;
END$$;

-- Detailed schedule immutable snapshots.
CREATE TABLE IF NOT EXISTS detailed_schedule_runs (
  id uuid PRIMARY KEY,
  start_date date NOT NULL,
  end_date date NOT NULL,
  horizon_days integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
  mode text NOT NULL DEFAULT 'DETAILED_HEURISTIC' CHECK (mode='DETAILED_HEURISTIC'),
  status text NOT NULL CHECK (status IN ('BUILDING','COMPLETE')),
  generated_at timestamptz NOT NULL DEFAULT now(),
  generated_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  generated_by text NOT NULL DEFAULT '',
  CHECK (end_date >= start_date)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_orders (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE CASCADE,
  source_type text NOT NULL CHECK (source_type IN ('FIRM_WO','MRP_PLANNED')),
  source_ref text NOT NULL,
  work_order_id uuid REFERENCES work_orders(id) ON DELETE RESTRICT,
  item_id uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  item_code text NOT NULL,
  quantity numeric NOT NULL CHECK (quantity >= 0),
  priority integer NOT NULL,
  earliest_start timestamptz NOT NULL,
  due_at timestamptz NOT NULL,
  scheduled_start timestamptz,
  scheduled_end timestamptz,
  schedule_status text NOT NULL CHECK (schedule_status IN ('ON_TIME','LATE','PARTIAL','UNSCHEDULED')),
  tardy_minutes numeric NOT NULL DEFAULT 0 CHECK (tardy_minutes >= 0),
  UNIQUE(run_id,source_type,source_ref)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_batches (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE CASCADE,
  schedule_order_id uuid NOT NULL REFERENCES detailed_schedule_orders(id) ON DELETE CASCADE,
  operation_seq integer NOT NULL,
  operation_desc text NOT NULL DEFAULT '',
  batch_no integer NOT NULL CHECK (batch_no > 0),
  batch_qty numeric NOT NULL CHECK (batch_qty > 0),
  cumulative_qty numeric NOT NULL CHECK (cumulative_qty > 0),
  setup_family text NOT NULL DEFAULT '',
  work_center_id uuid REFERENCES work_centers(id) ON DELETE RESTRICT,
  work_center_code text NOT NULL DEFAULT '',
  work_center_name text NOT NULL DEFAULT '',
  primary_work_center boolean NOT NULL DEFAULT true,
  alternative_priority integer NOT NULL DEFAULT 0,
  machine_capacity_snapshot integer NOT NULL DEFAULT 1 CHECK (machine_capacity_snapshot > 0),
  worker_capacity_snapshot integer NOT NULL DEFAULT 0 CHECK (worker_capacity_snapshot >= 0),
  machines_required integer NOT NULL DEFAULT 1 CHECK (machines_required > 0),
  workers_required integer NOT NULL DEFAULT 0 CHECK (workers_required >= 0),
  sequence_setup_minutes numeric NOT NULL DEFAULT 0 CHECK (sequence_setup_minutes >= 0),
  run_clock_minutes numeric NOT NULL DEFAULT 0 CHECK (run_clock_minutes >= 0),
  scheduled_start timestamptz,
  scheduled_end timestamptz,
  schedule_status text NOT NULL CHECK (schedule_status IN ('SCHEDULED','UNSCHEDULED')),
  UNIQUE(run_id,schedule_order_id,operation_seq,batch_no),
  CHECK ((scheduled_start IS NULL)=(scheduled_end IS NULL)),
  CHECK (scheduled_start IS NULL OR scheduled_end>scheduled_start),
  CHECK (machines_required <= machine_capacity_snapshot),
  CHECK (workers_required <= worker_capacity_snapshot)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_batch_dependencies (
  batch_id uuid NOT NULL REFERENCES detailed_schedule_batches(id) ON DELETE CASCADE,
  predecessor_batch_id uuid NOT NULL REFERENCES detailed_schedule_batches(id) ON DELETE CASCADE,
  dependency_type text NOT NULL CHECK (dependency_type IN ('ROUTING','SAME_OPERATION')),
  PRIMARY KEY(batch_id, predecessor_batch_id),
  CHECK (batch_id <> predecessor_batch_id)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_segments (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE CASCADE,
  batch_id uuid NOT NULL REFERENCES detailed_schedule_batches(id) ON DELETE CASCADE,
  schedule_order_id uuid NOT NULL REFERENCES detailed_schedule_orders(id) ON DELETE CASCADE,
  operation_seq integer NOT NULL,
  batch_no integer NOT NULL,
  segment_no integer NOT NULL CHECK (segment_no > 0),
  segment_type text NOT NULL CHECK (segment_type IN ('SETUP','RUN')),
  work_center_id uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  machines_required integer NOT NULL CHECK (machines_required > 0),
  workers_required integer NOT NULL CHECK (workers_required >= 0),
  machine_capacity_snapshot integer NOT NULL CHECK (machine_capacity_snapshot > 0),
  worker_capacity_snapshot integer NOT NULL CHECK (worker_capacity_snapshot >= 0),
  setup_family text NOT NULL DEFAULT '',
  from_setup_family text NOT NULL DEFAULT '',
  clock_minutes numeric NOT NULL CHECK (clock_minutes > 0),
  firm boolean NOT NULL DEFAULT false,
  CHECK (end_at>start_at),
  UNIQUE(run_id,batch_id,segment_no),
  CHECK (machines_required <= machine_capacity_snapshot),
  CHECK (workers_required <= worker_capacity_snapshot)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_machine_allocations (
  segment_id uuid NOT NULL REFERENCES detailed_schedule_segments(id) ON DELETE CASCADE,
  run_id uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE CASCADE,
  work_center_id uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  lane_no integer NOT NULL CHECK (lane_no > 0),
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  PRIMARY KEY(segment_id,lane_no),
  CHECK (end_at>start_at)
);

CREATE TABLE IF NOT EXISTS detailed_schedule_loads (
  run_id uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE CASCADE,
  work_center_id uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  work_center_code text NOT NULL,
  work_center_name text NOT NULL,
  load_date date NOT NULL,
  required_minutes numeric NOT NULL DEFAULT 0 CHECK (required_minutes >= 0),
  available_minutes numeric NOT NULL DEFAULT 0 CHECK (available_minutes >= 0),
  load_pct numeric NOT NULL DEFAULT 0 CHECK (load_pct >= 0),
  is_holiday boolean NOT NULL DEFAULT false,
  PRIMARY KEY(run_id,work_center_id,load_date)
);

CREATE INDEX IF NOT EXISTS detailed_segments_wc_time_idx
  ON detailed_schedule_segments(run_id,work_center_id,start_at,end_at);
CREATE INDEX IF NOT EXISTS detailed_machine_wc_lane_time_idx
  ON detailed_schedule_machine_allocations(run_id,work_center_id,lane_no,start_at,end_at);

CREATE OR REPLACE FUNCTION assert_detailed_schedule_run(p_run_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE r detailed_schedule_runs%ROWTYPE;
BEGIN
  SELECT * INTO r FROM detailed_schedule_runs WHERE id=p_run_id;
  IF NOT FOUND THEN RETURN; END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_segments s
    JOIN detailed_schedule_batches b ON b.id=s.batch_id
    WHERE s.run_id=p_run_id
      AND (s.run_id<>b.run_id OR s.schedule_order_id<>b.schedule_order_id OR s.operation_seq<>b.operation_seq OR s.batch_no<>b.batch_no)
  ) THEN
    RAISE EXCEPTION 'detailed schedule segment/batch identity mismatch' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_machine_allocations a
    JOIN detailed_schedule_segments s ON s.id=a.segment_id
    WHERE a.run_id=p_run_id
      AND (a.run_id<>s.run_id OR a.work_center_id<>s.work_center_id OR a.start_at<>s.start_at OR a.end_at<>s.end_at)
  ) THEN
    RAISE EXCEPTION 'detailed schedule machine allocation does not match segment identity/time' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_machine_allocations a
    JOIN detailed_schedule_machine_allocations b
      ON b.run_id=a.run_id AND b.work_center_id=a.work_center_id AND b.lane_no=a.lane_no AND b.segment_id>a.segment_id
     AND tstzrange(a.start_at,a.end_at,'[)') && tstzrange(b.start_at,b.end_at,'[)')
    WHERE a.run_id=p_run_id
  ) THEN
    RAISE EXCEPTION 'detailed schedule contains overlapping use of the same machine lane' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_segments s
     WHERE s.run_id=p_run_id AND (
       SELECT count(*) FROM detailed_schedule_machine_allocations a WHERE a.segment_id=s.id
     ) <> s.machines_required
  ) THEN
    RAISE EXCEPTION 'detailed schedule machine allocation count does not match machines_required' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_machine_allocations a
    JOIN detailed_schedule_segments s ON s.id=a.segment_id
    WHERE a.run_id=p_run_id AND a.lane_no>s.machine_capacity_snapshot
  ) THEN
    RAISE EXCEPTION 'detailed schedule machine lane exceeds machine capacity snapshot' USING ERRCODE='23514';
  END IF;

  -- Worker demand can only rise at a segment start, so checking every start event
  -- is sufficient for cumulative head-count capacity.
  IF EXISTS (
    SELECT 1 FROM detailed_schedule_segments p
     WHERE p.run_id=p_run_id AND (
       SELECT COALESCE(sum(q.workers_required),0)
         FROM detailed_schedule_segments q
        WHERE q.run_id=p.run_id AND q.work_center_id=p.work_center_id
          AND q.start_at<=p.start_at AND q.end_at>p.start_at
     ) > p.worker_capacity_snapshot
  ) THEN
    RAISE EXCEPTION 'detailed schedule exceeds worker capacity' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_batch_dependencies d
    JOIN detailed_schedule_batches b ON b.id=d.batch_id
    JOIN detailed_schedule_batches p ON p.id=d.predecessor_batch_id
    WHERE b.run_id=p_run_id
      AND (p.run_id<>b.run_id OR p.schedule_order_id<>b.schedule_order_id
           OR p.operation_seq>b.operation_seq
           OR (b.schedule_status='SCHEDULED' AND p.schedule_status<>'SCHEDULED')
           OR (b.schedule_status='SCHEDULED' AND b.scheduled_start < p.scheduled_end))
  ) THEN
    RAISE EXCEPTION 'detailed schedule violates batch precedence / transfer dependency' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_batches b
    LEFT JOIN LATERAL (
      SELECT min(s.start_at) mn,max(s.end_at) mx FROM detailed_schedule_segments s WHERE s.batch_id=b.id
    ) x ON true
    WHERE b.run_id=p_run_id AND b.schedule_status='SCHEDULED'
      AND (x.mn IS NULL OR b.scheduled_start<>x.mn OR b.scheduled_end<>x.mx)
  ) THEN
    RAISE EXCEPTION 'detailed schedule batch start/end does not match segment range' USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM detailed_schedule_segments s
     WHERE s.run_id=p_run_id
       AND (s.start_at::date<r.start_date OR s.end_at::date>r.end_date+1)
  ) THEN
    RAISE EXCEPTION 'detailed schedule segment falls outside horizon' USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION detailed_schedule_guard()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid;
BEGIN
  rid := COALESCE(NEW.run_id,OLD.run_id);
  PERFORM assert_detailed_schedule_run(rid);
  RETURN COALESCE(NEW,OLD);
END$$;

DROP TRIGGER IF EXISTS trg_detailed_segments_guard ON detailed_schedule_segments;
CREATE CONSTRAINT TRIGGER trg_detailed_segments_guard
AFTER INSERT OR UPDATE OR DELETE ON detailed_schedule_segments
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION detailed_schedule_guard();
DROP TRIGGER IF EXISTS trg_detailed_machine_guard ON detailed_schedule_machine_allocations;
CREATE CONSTRAINT TRIGGER trg_detailed_machine_guard
AFTER INSERT OR UPDATE OR DELETE ON detailed_schedule_machine_allocations
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION detailed_schedule_guard();
DROP TRIGGER IF EXISTS trg_detailed_batch_guard ON detailed_schedule_batches;
CREATE CONSTRAINT TRIGGER trg_detailed_batch_guard
AFTER INSERT OR UPDATE OR DELETE ON detailed_schedule_batches
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION detailed_schedule_guard();
DROP TRIGGER IF EXISTS trg_detailed_dependency_guard ON detailed_schedule_batch_dependencies;
CREATE CONSTRAINT TRIGGER trg_detailed_dependency_guard
AFTER INSERT OR UPDATE OR DELETE ON detailed_schedule_batch_dependencies
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION detailed_schedule_guard();

CREATE OR REPLACE FUNCTION protect_completed_detailed_snapshot()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid; st text;
BEGIN
  rid := COALESCE(OLD.run_id,NEW.run_id);
  SELECT status INTO st FROM detailed_schedule_runs WHERE id=rid;
  IF st='COMPLETE' THEN
    RAISE EXCEPTION 'completed detailed schedule snapshot is immutable' USING ERRCODE='23514';
  END IF;
  RETURN COALESCE(NEW,OLD);
END$$;

DROP TRIGGER IF EXISTS trg_detailed_orders_immutable ON detailed_schedule_orders;
CREATE TRIGGER trg_detailed_orders_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_orders
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_snapshot();
DROP TRIGGER IF EXISTS trg_detailed_batches_immutable ON detailed_schedule_batches;
CREATE TRIGGER trg_detailed_batches_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_batches
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_snapshot();
DROP TRIGGER IF EXISTS trg_detailed_segments_immutable ON detailed_schedule_segments;
CREATE TRIGGER trg_detailed_segments_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_segments
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_snapshot();
DROP TRIGGER IF EXISTS trg_detailed_machine_immutable ON detailed_schedule_machine_allocations;
CREATE TRIGGER trg_detailed_machine_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_machine_allocations
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_snapshot();
DROP TRIGGER IF EXISTS trg_detailed_loads_immutable ON detailed_schedule_loads;
CREATE TRIGGER trg_detailed_loads_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_loads
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_snapshot();

CREATE OR REPLACE FUNCTION protect_completed_detailed_dependency()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid; st text; bid uuid;
BEGIN
  bid:=COALESCE(OLD.batch_id,NEW.batch_id);
  SELECT b.run_id INTO rid FROM detailed_schedule_batches b WHERE b.id=bid;
  SELECT status INTO st FROM detailed_schedule_runs WHERE id=rid;
  IF st='COMPLETE' THEN
    RAISE EXCEPTION 'completed detailed schedule dependencies are immutable' USING ERRCODE='23514';
  END IF;
  RETURN COALESCE(NEW,OLD);
END$$;
DROP TRIGGER IF EXISTS trg_detailed_dependencies_immutable ON detailed_schedule_batch_dependencies;
CREATE TRIGGER trg_detailed_dependencies_immutable BEFORE UPDATE OR DELETE ON detailed_schedule_batch_dependencies
FOR EACH ROW EXECUTE FUNCTION protect_completed_detailed_dependency();

CREATE OR REPLACE FUNCTION validate_detailed_run_completion()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status='COMPLETE' AND NEW IS DISTINCT FROM OLD THEN
    RAISE EXCEPTION 'completed detailed schedule run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status='COMPLETE' AND OLD.status<>'COMPLETE' THEN
    PERFORM assert_detailed_schedule_run(NEW.id);
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS trg_detailed_run_completion ON detailed_schedule_runs;
CREATE TRIGGER trg_detailed_run_completion BEFORE UPDATE ON detailed_schedule_runs
FOR EACH ROW EXECUTE FUNCTION validate_detailed_run_completion();

COMMIT;
