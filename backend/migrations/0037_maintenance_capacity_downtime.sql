-- 0037: Maintenance + Capacity Downtime
--
-- Preventive maintenance, breakdowns, planned downtime and unplanned downtime
-- are revisioned, append-only capacity evidence. Detailed Scheduling and CTP
-- consume the same current maintenance view; persisted detailed schedules freeze
-- the exact revisions used. Full Pegging traces those events as capacity root
-- causes.

CREATE TABLE maintenance_events (
  id                 uuid PRIMARY KEY,
  work_center_id     uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  event_type         text NOT NULL CHECK (event_type IN (
                       'PREVENTIVE_MAINTENANCE','BREAKDOWN','PLANNED_DOWNTIME','UNPLANNED_DOWNTIME')),
  created_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_by         text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX maintenance_events_wc_idx ON maintenance_events(work_center_id,created_at DESC,id);

CREATE TABLE maintenance_event_revisions (
  id                    uuid PRIMARY KEY,
  maintenance_event_id  uuid NOT NULL REFERENCES maintenance_events(id) ON DELETE RESTRICT,
  revision_no           integer NOT NULL CHECK (revision_no > 0),
  status                text NOT NULL CHECK (status IN ('PLANNED','ACTIVE','COMPLETED','CANCELLED')),
  start_at              timestamptz NOT NULL,
  end_at                timestamptz NOT NULL,
  unavailable_machines  integer NOT NULL DEFAULT 0 CHECK (unavailable_machines >= 0),
  unavailable_workers   integer NOT NULL DEFAULT 0 CHECK (unavailable_workers >= 0),
  reason                text NOT NULL DEFAULT '',
  source_ref            text NOT NULL DEFAULT '',
  actor_user_id         uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  actor_username        text NOT NULL,
  occurred_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE(maintenance_event_id,revision_no),
  CHECK (end_at > start_at),
  CHECK (unavailable_machines > 0 OR unavailable_workers > 0 OR status IN ('COMPLETED','CANCELLED')),
  CHECK (status <> 'CANCELLED' OR (unavailable_machines=0 AND unavailable_workers=0))
);
CREATE INDEX maintenance_revision_event_idx ON maintenance_event_revisions(maintenance_event_id,revision_no DESC);
CREATE INDEX maintenance_revision_window_idx ON maintenance_event_revisions(start_at,end_at,status);

CREATE OR REPLACE FUNCTION guard_maintenance_actor(p_user uuid,p_name text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE urole text; uname text; active boolean;
BEGIN
  SELECT role,username,is_active INTO urole,uname,active FROM users WHERE id=p_user;
  IF urole IS NULL OR NOT active OR urole NOT IN ('planner','admin') OR uname IS DISTINCT FROM p_name THEN
    RAISE EXCEPTION 'maintenance mutation requires matching active planner/admin actor' USING ERRCODE='42501';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION reject_maintenance_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'maintenance event identity is immutable' USING ERRCODE='23514';
END$$;
CREATE TRIGGER maintenance_events_append_only_trg
BEFORE UPDATE OR DELETE ON maintenance_events
FOR EACH ROW EXECUTE FUNCTION reject_maintenance_event_mutation();

CREATE OR REPLACE FUNCTION guard_maintenance_event_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM guard_maintenance_actor(NEW.created_by_user_id,NEW.created_by);
  RETURN NEW;
END$$;
CREATE TRIGGER maintenance_events_insert_guard_trg
BEFORE INSERT ON maintenance_events FOR EACH ROW EXECUTE FUNCTION guard_maintenance_event_insert();

CREATE OR REPLACE FUNCTION guard_maintenance_revision_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE expected_rev integer; mc integer; wc integer; prior text;
BEGIN
  PERFORM guard_maintenance_actor(NEW.actor_user_id,NEW.actor_username);
  SELECT w.machine_count,w.worker_count INTO mc,wc
    FROM maintenance_events e JOIN work_centers w ON w.id=e.work_center_id
   WHERE e.id=NEW.maintenance_event_id FOR SHARE OF e,w;
  IF mc IS NULL THEN
    RAISE EXCEPTION 'maintenance event/work center not found' USING ERRCODE='23503';
  END IF;
  IF NEW.unavailable_machines > GREATEST(mc,1) OR NEW.unavailable_workers > GREATEST(wc,0) THEN
    RAISE EXCEPTION 'maintenance capacity reduction exceeds work center resources' USING ERRCODE='23514';
  END IF;
  SELECT COALESCE(MAX(revision_no),0)+1 INTO expected_rev
    FROM maintenance_event_revisions WHERE maintenance_event_id=NEW.maintenance_event_id;
  IF NEW.revision_no<>expected_rev THEN
    RAISE EXCEPTION 'maintenance revision must be sequential: expected %',expected_rev USING ERRCODE='23514';
  END IF;
  SELECT status INTO prior FROM maintenance_event_revisions
   WHERE maintenance_event_id=NEW.maintenance_event_id ORDER BY revision_no DESC LIMIT 1;
  IF prior IN ('COMPLETED','CANCELLED') THEN
    RAISE EXCEPTION 'completed/cancelled maintenance event is terminal' USING ERRCODE='23514';
  END IF;
  IF prior IS NULL AND NEW.status NOT IN ('PLANNED','ACTIVE') THEN
    RAISE EXCEPTION 'first maintenance revision must be PLANNED or ACTIVE' USING ERRCODE='23514';
  END IF;
  IF prior='ACTIVE' AND NEW.status='PLANNED' THEN
    RAISE EXCEPTION 'ACTIVE maintenance cannot return to PLANNED' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER maintenance_revision_insert_guard_trg
BEFORE INSERT ON maintenance_event_revisions FOR EACH ROW EXECUTE FUNCTION guard_maintenance_revision_insert();

CREATE OR REPLACE FUNCTION reject_maintenance_revision_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'maintenance revisions are append-only evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER maintenance_revisions_append_only_trg
BEFORE UPDATE OR DELETE ON maintenance_event_revisions
FOR EACH ROW EXECUTE FUNCTION reject_maintenance_revision_mutation();

CREATE OR REPLACE VIEW v_current_maintenance_events AS
SELECT e.id,e.work_center_id,w.code AS work_center_code,w.name AS work_center_name,e.event_type,
       r.id AS revision_id,r.revision_no,r.status,r.start_at,r.end_at,
       r.unavailable_machines,r.unavailable_workers,r.reason,r.source_ref,
       e.created_by_user_id,e.created_by,e.created_at,
       r.actor_user_id,r.actor_username,r.occurred_at
  FROM maintenance_events e
  JOIN work_centers w ON w.id=e.work_center_id
  JOIN LATERAL (
    SELECT x.* FROM maintenance_event_revisions x
     WHERE x.maintenance_event_id=e.id ORDER BY x.revision_no DESC LIMIT 1
  ) r ON true;

CREATE OR REPLACE VIEW v_effective_maintenance_capacity AS
SELECT * FROM v_current_maintenance_events
 WHERE status IN ('PLANNED','ACTIVE');

-- 0030 attached the shared detailed_schedule_guard() to
-- detailed_schedule_batch_dependencies even though that table has no run_id column.
-- Derive the run through batch_id for dependency rows while preserving the existing
-- behavior for detailed schedule child tables that do carry run_id.  Keeping this
-- repair in the newest migration avoids changing the checksum of accepted migration 0030.
CREATE OR REPLACE FUNCTION detailed_schedule_guard()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rid uuid; bid uuid;
BEGIN
  IF TG_TABLE_NAME='detailed_schedule_batch_dependencies' THEN
    IF TG_OP='DELETE' THEN
      bid := OLD.batch_id;
    ELSE
      bid := NEW.batch_id;
    END IF;
    SELECT run_id INTO rid FROM detailed_schedule_batches WHERE id=bid;
  ELSE
    IF TG_OP='DELETE' THEN
      rid := OLD.run_id;
    ELSE
      rid := NEW.run_id;
    END IF;
  END IF;
  IF rid IS NULL THEN
    RAISE EXCEPTION 'detailed schedule guard cannot resolve run id' USING ERRCODE='23514';
  END IF;
  PERFORM assert_detailed_schedule_run(rid);
  IF TG_OP='DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END$$;

-- Every persisted detailed schedule freezes the exact maintenance revisions used.
CREATE TABLE detailed_schedule_maintenance_snapshots (
  run_id                uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  maintenance_event_id  uuid NOT NULL REFERENCES maintenance_events(id) ON DELETE RESTRICT,
  revision_id           uuid NOT NULL REFERENCES maintenance_event_revisions(id) ON DELETE RESTRICT,
  revision_no           integer NOT NULL,
  work_center_id        uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  event_type            text NOT NULL,
  status                text NOT NULL,
  start_at              timestamptz NOT NULL,
  end_at                timestamptz NOT NULL,
  unavailable_machines  integer NOT NULL,
  unavailable_workers   integer NOT NULL,
  reason                text NOT NULL,
  source_ref            text NOT NULL,
  PRIMARY KEY(run_id,maintenance_event_id)
);
CREATE INDEX detailed_schedule_maintenance_wc_idx
  ON detailed_schedule_maintenance_snapshots(run_id,work_center_id,start_at,end_at);

CREATE OR REPLACE FUNCTION reject_detailed_maintenance_snapshot_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'detailed maintenance snapshots are immutable evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER detailed_maintenance_snapshot_append_only_trg
BEFORE UPDATE OR DELETE ON detailed_schedule_maintenance_snapshots
FOR EACH ROW EXECUTE FUNCTION reject_detailed_maintenance_snapshot_mutation();

-- Extend Full Pegging vocabulary for maintenance root causes.
ALTER TABLE pegging_nodes DROP CONSTRAINT IF EXISTS pegging_nodes_node_type_check;
ALTER TABLE pegging_nodes ADD CONSTRAINT pegging_nodes_node_type_check CHECK (node_type IN (
  'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
  'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
  'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE',
  'SUPPLIER_CONFIRMATION','SUPPLIER_ASN','LEAD_TIME_PROFILE','INVENTORY_POLICY','MAINTENANCE_EVENT'
));

ALTER TABLE pegging_edges DROP CONSTRAINT IF EXISTS pegging_edges_edge_type_check;
ALTER TABLE pegging_edges ADD CONSTRAINT pegging_edges_edge_type_check CHECK (edge_type IN (
  'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
  'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
  'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY',
  'CONFIRMED_BY','SHIPPED_BY','PLANNED_USING','PROTECTED_BY','CAPACITY_REDUCED_BY'
));

ALTER TABLE planning_exceptions DROP CONSTRAINT IF EXISTS planning_exceptions_exception_type_check;
ALTER TABLE planning_exceptions ADD CONSTRAINT planning_exceptions_exception_type_check CHECK (exception_type IN (
  'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
  'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
  'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED',
  'SUPPLIER_CONFIRMATION_LATE','SUPPLIER_RELIABILITY_RISK',
  'SAFETY_STOCK_BREACH','REORDER_POINT_BREACH',
  'PREVENTIVE_MAINTENANCE_CAPACITY','BREAKDOWN_CAPACITY',
  'PLANNED_DOWNTIME_CAPACITY','UNPLANNED_DOWNTIME_CAPACITY'
));
