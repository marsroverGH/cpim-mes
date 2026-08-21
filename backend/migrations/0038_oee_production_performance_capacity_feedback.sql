-- ============================================================================
-- 0038: OEE + Production Performance + Actual Capacity Feedback
--
-- Closes the planning/execution loop using existing Shop Floor actuals,
-- operation logs, routing standards and maintenance evidence. Calculation runs
-- are immutable snapshots. Capacity feedback is versioned and planner-owned;
-- Detailed Scheduling / CTP freeze the exact ACTIVE version that influenced a
-- finite-capacity decision.
-- ============================================================================

CREATE TABLE production_performance_runs (
  id                    uuid PRIMARY KEY,
  window_start          date NOT NULL,
  window_end            date NOT NULL,
  min_completed_ops     integer NOT NULL DEFAULT 1 CHECK (min_completed_ops >= 1),
  status                text NOT NULL DEFAULT 'RUNNING' CHECK (status IN ('RUNNING','COMPLETE','FAILED')),
  result_hash           text,
  generated_by_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  generated_by          text NOT NULL,
  completed_at          timestamptz,
  error_text            text NOT NULL DEFAULT '',
  created_at            timestamptz NOT NULL DEFAULT now(),
  CHECK (window_end >= window_start),
  CHECK (result_hash IS NULL OR length(result_hash)=64)
);

CREATE TABLE production_performance_results (
  id                         uuid PRIMARY KEY,
  run_id                     uuid NOT NULL REFERENCES production_performance_runs(id) ON DELETE RESTRICT,
  work_center_id             uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  work_center_code           text NOT NULL,
  sample_count               integer NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
  planned_production_minutes numeric NOT NULL DEFAULT 0 CHECK (planned_production_minutes >= 0),
  run_time_minutes           numeric NOT NULL DEFAULT 0 CHECK (run_time_minutes >= 0),
  downtime_minutes           numeric NOT NULL DEFAULT 0 CHECK (downtime_minutes >= 0),
  active_session_minutes     numeric NOT NULL DEFAULT 0 CHECK (active_session_minutes >= 0),
  planned_setup_minutes      numeric NOT NULL DEFAULT 0 CHECK (planned_setup_minutes >= 0),
  ideal_run_minutes          numeric NOT NULL DEFAULT 0 CHECK (ideal_run_minutes >= 0),
  pause_minutes              numeric NOT NULL DEFAULT 0 CHECK (pause_minutes >= 0),
  planned_downtime_minutes   numeric NOT NULL DEFAULT 0 CHECK (planned_downtime_minutes >= 0),
  unplanned_downtime_minutes numeric NOT NULL DEFAULT 0 CHECK (unplanned_downtime_minutes >= 0),
  setup_loss_minutes         numeric NOT NULL DEFAULT 0 CHECK (setup_loss_minutes >= 0),
  speed_loss_minutes         numeric NOT NULL DEFAULT 0 CHECK (speed_loss_minutes >= 0),
  good_quantity              numeric NOT NULL DEFAULT 0 CHECK (good_quantity >= 0),
  reject_quantity            numeric NOT NULL DEFAULT 0 CHECK (reject_quantity >= 0),
  availability               numeric NOT NULL DEFAULT 0 CHECK (availability >= 0 AND availability <= 1),
  performance                numeric NOT NULL DEFAULT 0 CHECK (performance >= 0 AND performance <= 1.5),
  quality                    numeric NOT NULL DEFAULT 0 CHECK (quality >= 0 AND quality <= 1),
  oee                        numeric NOT NULL DEFAULT 0 CHECK (oee >= 0 AND oee <= 1),
  breakdown_count            integer NOT NULL DEFAULT 0 CHECK (breakdown_count >= 0),
  mtbf_minutes               numeric NOT NULL DEFAULT 0 CHECK (mtbf_minutes >= 0),
  mttr_minutes               numeric NOT NULL DEFAULT 0 CHECK (mttr_minutes >= 0),
  recommended_efficiency     numeric NOT NULL CHECK (recommended_efficiency > 0 AND recommended_efficiency <= 1.2),
  recommended_utilization    numeric NOT NULL CHECK (recommended_utilization > 0 AND recommended_utilization <= 1),
  confidence                 text NOT NULL CHECK (confidence IN ('LOW','MEDIUM','HIGH')),
  created_at                 timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,work_center_id)
);
CREATE INDEX production_performance_results_wc_idx ON production_performance_results(work_center_id,created_at DESC);

CREATE TABLE capacity_feedback_versions (
  id                       uuid PRIMARY KEY,
  work_center_id           uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  version_no               integer NOT NULL CHECK (version_no >= 1),
  source_run_id            uuid NOT NULL REFERENCES production_performance_runs(id) ON DELETE RESTRICT,
  source_result_id         uuid NOT NULL REFERENCES production_performance_results(id) ON DELETE RESTRICT,
  status                   text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
  effective_efficiency     numeric NOT NULL CHECK (effective_efficiency > 0 AND effective_efficiency <= 1.2),
  effective_utilization    numeric NOT NULL CHECK (effective_utilization > 0 AND effective_utilization <= 1),
  source_oee               numeric NOT NULL CHECK (source_oee >= 0 AND source_oee <= 1),
  source_availability      numeric NOT NULL CHECK (source_availability >= 0 AND source_availability <= 1),
  source_performance       numeric NOT NULL CHECK (source_performance >= 0 AND source_performance <= 1.5),
  source_quality           numeric NOT NULL CHECK (source_quality >= 0 AND source_quality <= 1),
  sample_count             integer NOT NULL CHECK (sample_count >= 0),
  confidence               text NOT NULL CHECK (confidence IN ('LOW','MEDIUM','HIGH')),
  effective_from           date NOT NULL DEFAULT current_date,
  notes                    text NOT NULL DEFAULT '',
  created_by_user_id       uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_by               text NOT NULL,
  created_at               timestamptz NOT NULL DEFAULT now(),
  activated_by_user_id     uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by             text,
  activated_at             timestamptz,
  archived_by_user_id      uuid REFERENCES users(id) ON DELETE RESTRICT,
  archived_by              text,
  archived_at              timestamptz,
  UNIQUE(work_center_id,version_no)
);
CREATE UNIQUE INDEX capacity_feedback_one_active_uq ON capacity_feedback_versions(work_center_id) WHERE status='ACTIVE';
CREATE INDEX capacity_feedback_effective_idx ON capacity_feedback_versions(work_center_id,effective_from,status);

CREATE VIEW v_current_capacity_feedback AS
SELECT f.*,w.code AS work_center_code,w.name AS work_center_name
  FROM capacity_feedback_versions f
  JOIN work_centers w ON w.id=f.work_center_id
 WHERE f.status='ACTIVE'
   AND f.effective_from <= (now() AT TIME ZONE eco_business_timezone())::date;

CREATE TABLE detailed_schedule_capacity_feedback_snapshots (
  run_id                 uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  feedback_version_id    uuid NOT NULL REFERENCES capacity_feedback_versions(id) ON DELETE RESTRICT,
  work_center_id         uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  version_no             integer NOT NULL,
  source_run_id          uuid NOT NULL REFERENCES production_performance_runs(id) ON DELETE RESTRICT,
  source_result_id       uuid NOT NULL REFERENCES production_performance_results(id) ON DELETE RESTRICT,
  effective_efficiency   numeric NOT NULL,
  effective_utilization  numeric NOT NULL,
  source_oee             numeric NOT NULL,
  source_availability    numeric NOT NULL,
  source_performance     numeric NOT NULL,
  source_quality         numeric NOT NULL,
  sample_count           integer NOT NULL,
  confidence             text NOT NULL,
  effective_from         date NOT NULL,
  PRIMARY KEY(run_id,work_center_id)
);
CREATE INDEX detailed_capacity_feedback_version_idx ON detailed_schedule_capacity_feedback_snapshots(feedback_version_id,run_id);

CREATE OR REPLACE FUNCTION validate_production_performance_actor(p_user uuid,p_username text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE r text; u text; active boolean;
BEGIN
  SELECT role,username,is_active INTO r,u,active FROM users WHERE id=p_user;
  IF r IS NULL OR NOT active OR u IS DISTINCT FROM p_username OR r NOT IN ('admin','planner') THEN
    RAISE EXCEPTION 'production performance actor must be matching active planner/admin' USING ERRCODE='42501';
  END IF;
END$$;

-- OEE evidence must remain auditable. Existing Shop Floor code only INSERTs
-- operation_logs, so UPDATE/DELETE can now be rejected safely.
CREATE OR REPLACE FUNCTION reject_operation_log_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'operation logs are immutable production evidence' USING ERRCODE='23514';
END$$;
DROP TRIGGER IF EXISTS operation_logs_append_only_trg ON operation_logs;
CREATE TRIGGER operation_logs_append_only_trg
BEFORE UPDATE OR DELETE ON operation_logs
FOR EACH ROW EXECUTE FUNCTION reject_operation_log_mutation();
CREATE INDEX IF NOT EXISTS operation_logs_event_time_idx ON operation_logs(event_at,event_type,wo_op_id);

CREATE OR REPLACE FUNCTION reject_production_performance_result_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'production performance results are immutable evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER production_performance_result_append_only_trg
BEFORE UPDATE OR DELETE ON production_performance_results
FOR EACH ROW EXECUTE FUNCTION reject_production_performance_result_mutation();

CREATE OR REPLACE FUNCTION guard_production_performance_run_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM validate_production_performance_actor(NEW.generated_by_user_id,NEW.generated_by);
  IF NEW.status<>'RUNNING' OR NEW.result_hash IS NOT NULL OR NEW.completed_at IS NOT NULL OR COALESCE(NEW.error_text,'')<>'' THEN
    RAISE EXCEPTION 'production performance run must start RUNNING without result evidence' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER production_performance_run_insert_guard_trg
BEFORE INSERT ON production_performance_runs
FOR EACH ROW EXECUTE FUNCTION guard_production_performance_run_insert();

CREATE OR REPLACE FUNCTION guard_production_performance_run()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('COMPLETE','FAILED') THEN
    RAISE EXCEPTION 'completed production performance run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.window_start IS DISTINCT FROM OLD.window_start OR
     NEW.window_end IS DISTINCT FROM OLD.window_end OR NEW.min_completed_ops IS DISTINCT FROM OLD.min_completed_ops OR
     NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR NEW.generated_by IS DISTINCT FROM OLD.generated_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'production performance run identity is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER production_performance_run_guard_trg
BEFORE UPDATE ON production_performance_runs
FOR EACH ROW EXECUTE FUNCTION guard_production_performance_run();

CREATE OR REPLACE FUNCTION guard_production_performance_result_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM production_performance_runs WHERE id=NEW.run_id FOR SHARE;
  IF s IS DISTINCT FROM 'RUNNING' THEN
    RAISE EXCEPTION 'performance results may only be inserted while run is RUNNING' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER production_performance_result_insert_guard_trg
BEFORE INSERT ON production_performance_results
FOR EACH ROW EXECUTE FUNCTION guard_production_performance_result_insert();

CREATE OR REPLACE FUNCTION validate_capacity_feedback_actor(p_user uuid,p_username text)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  PERFORM validate_production_performance_actor(p_user,p_username);
END$$;

CREATE OR REPLACE FUNCTION guard_capacity_feedback_version()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_run uuid; source_wc uuid; run_status text;
BEGIN
  IF TG_OP='INSERT' THEN
    PERFORM validate_capacity_feedback_actor(NEW.created_by_user_id,NEW.created_by);
    IF NEW.status<>'DRAFT' OR NEW.activated_by_user_id IS NOT NULL OR NEW.activated_by IS NOT NULL OR NEW.activated_at IS NOT NULL OR
       NEW.archived_by_user_id IS NOT NULL OR NEW.archived_by IS NOT NULL OR NEW.archived_at IS NOT NULL THEN
      RAISE EXCEPTION 'capacity feedback version must be created as unactioned DRAFT' USING ERRCODE='23514';
    END IF;
    SELECT run_id,work_center_id INTO source_run,source_wc
      FROM production_performance_results WHERE id=NEW.source_result_id;
    IF source_run IS NULL OR source_run IS DISTINCT FROM NEW.source_run_id OR source_wc IS DISTINCT FROM NEW.work_center_id THEN
      RAISE EXCEPTION 'capacity feedback source result/run/work center provenance mismatch' USING ERRCODE='23514';
    END IF;
    SELECT status INTO run_status FROM production_performance_runs WHERE id=NEW.source_run_id;
    IF run_status IS DISTINCT FROM 'COMPLETE' THEN
      RAISE EXCEPTION 'capacity feedback requires a COMPLETE production performance run' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.work_center_id IS DISTINCT FROM OLD.work_center_id OR
     NEW.version_no IS DISTINCT FROM OLD.version_no OR NEW.source_run_id IS DISTINCT FROM OLD.source_run_id OR
     NEW.source_result_id IS DISTINCT FROM OLD.source_result_id OR NEW.effective_efficiency IS DISTINCT FROM OLD.effective_efficiency OR
     NEW.effective_utilization IS DISTINCT FROM OLD.effective_utilization OR NEW.source_oee IS DISTINCT FROM OLD.source_oee OR
     NEW.source_availability IS DISTINCT FROM OLD.source_availability OR NEW.source_performance IS DISTINCT FROM OLD.source_performance OR
     NEW.source_quality IS DISTINCT FROM OLD.source_quality OR NEW.sample_count IS DISTINCT FROM OLD.sample_count OR
     NEW.confidence IS DISTINCT FROM OLD.confidence OR NEW.effective_from IS DISTINCT FROM OLD.effective_from OR
     NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'capacity feedback evidence/configuration is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status='ARCHIVED' THEN
    RAISE EXCEPTION 'archived capacity feedback is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status='ACTIVE' AND NEW.status NOT IN ('ACTIVE','ARCHIVED') THEN
    RAISE EXCEPTION 'ACTIVE capacity feedback may only remain ACTIVE or become ARCHIVED' USING ERRCODE='23514';
  END IF;
  IF OLD.status='DRAFT' AND NEW.status NOT IN ('DRAFT','ACTIVE','ARCHIVED') THEN
    RAISE EXCEPTION 'invalid capacity feedback transition' USING ERRCODE='23514';
  END IF;
  IF NEW.status='ACTIVE' THEN
    IF NEW.effective_from > (now() AT TIME ZONE eco_business_timezone())::date THEN
      RAISE EXCEPTION 'future-effective capacity feedback cannot be activated yet' USING ERRCODE='23514';
    END IF;
    IF NEW.activated_by_user_id IS NULL OR NEW.activated_by IS NULL OR NEW.activated_at IS NULL THEN
      RAISE EXCEPTION 'capacity feedback activation audit is required' USING ERRCODE='23514';
    END IF;
    PERFORM validate_capacity_feedback_actor(NEW.activated_by_user_id,NEW.activated_by);
  END IF;
  IF NEW.status='ARCHIVED' THEN
    IF NEW.archived_by_user_id IS NULL OR NEW.archived_by IS NULL OR NEW.archived_at IS NULL THEN
      RAISE EXCEPTION 'capacity feedback archive audit is required' USING ERRCODE='23514';
    END IF;
    PERFORM validate_capacity_feedback_actor(NEW.archived_by_user_id,NEW.archived_by);
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER capacity_feedback_version_guard_trg
BEFORE INSERT OR UPDATE ON capacity_feedback_versions
FOR EACH ROW EXECUTE FUNCTION guard_capacity_feedback_version();

CREATE OR REPLACE FUNCTION reject_capacity_feedback_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'capacity feedback versions are immutable audit evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER capacity_feedback_no_delete_trg
BEFORE DELETE ON capacity_feedback_versions
FOR EACH ROW EXECUTE FUNCTION reject_capacity_feedback_delete();

CREATE OR REPLACE FUNCTION reject_detailed_capacity_feedback_snapshot_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'detailed capacity feedback snapshots are immutable evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER detailed_capacity_feedback_snapshot_append_only_trg
BEFORE UPDATE OR DELETE ON detailed_schedule_capacity_feedback_snapshots
FOR EACH ROW EXECUTE FUNCTION reject_detailed_capacity_feedback_snapshot_mutation();

-- Extend Full Pegging vocabulary for actual-capacity feedback root cause.
ALTER TABLE pegging_nodes DROP CONSTRAINT IF EXISTS pegging_nodes_node_type_check;
ALTER TABLE pegging_nodes ADD CONSTRAINT pegging_nodes_node_type_check CHECK (node_type IN (
  'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
  'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
  'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE',
  'SUPPLIER_CONFIRMATION','SUPPLIER_ASN','LEAD_TIME_PROFILE','INVENTORY_POLICY','MAINTENANCE_EVENT','CAPACITY_FEEDBACK'
));

ALTER TABLE pegging_edges DROP CONSTRAINT IF EXISTS pegging_edges_edge_type_check;
ALTER TABLE pegging_edges ADD CONSTRAINT pegging_edges_edge_type_check CHECK (edge_type IN (
  'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
  'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
  'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY',
  'CONFIRMED_BY','SHIPPED_BY','PLANNED_USING','PROTECTED_BY','CAPACITY_REDUCED_BY','CALIBRATED_BY'
));

ALTER TABLE planning_exceptions DROP CONSTRAINT IF EXISTS planning_exceptions_exception_type_check;
ALTER TABLE planning_exceptions ADD CONSTRAINT planning_exceptions_exception_type_check CHECK (exception_type IN (
  'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
  'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
  'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED',
  'SUPPLIER_CONFIRMATION_LATE','SUPPLIER_RELIABILITY_RISK',
  'SAFETY_STOCK_BREACH','REORDER_POINT_BREACH',
  'PREVENTIVE_MAINTENANCE_CAPACITY','BREAKDOWN_CAPACITY',
  'PLANNED_DOWNTIME_CAPACITY','UNPLANNED_DOWNTIME_CAPACITY','OEE_CAPACITY_RISK'
));
