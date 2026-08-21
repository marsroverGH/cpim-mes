-- 0039: Real-Time Dispatching + Dynamic Rescheduling + Schedule Adherence
--
-- Promote Detailed Scheduling from a static planning snapshot into a controlled
-- execution schedule. A single active execution run drives live dispatching.
-- Rescheduling always creates a new immutable Detailed Schedule candidate first;
-- frozen-horizon changes block activation, firm/flexible changes are audited,
-- and every activation is append-only evidence. Schedule adherence snapshots
-- reconcile planned times with Shop Floor actuals.

CREATE TABLE dispatch_policy_versions (
  id                                uuid PRIMARY KEY,
  version_no                        integer NOT NULL UNIQUE CHECK (version_no > 0),
  status                            text NOT NULL CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
  freeze_minutes                    integer NOT NULL DEFAULT 240 CHECK (freeze_minutes >= 0),
  firm_minutes                      integer NOT NULL DEFAULT 1440 CHECK (firm_minutes >= freeze_minutes),
  start_late_threshold_minutes      integer NOT NULL DEFAULT 30 CHECK (start_late_threshold_minutes >= 0),
  completion_late_threshold_minutes integer NOT NULL DEFAULT 30 CHECK (completion_late_threshold_minutes >= 0),
  auto_reschedule                   boolean NOT NULL DEFAULT true,
  min_auto_interval_minutes         integer NOT NULL DEFAULT 15 CHECK (min_auto_interval_minutes >= 0),
  setup_match_bonus                 numeric NOT NULL DEFAULT 20 CHECK (setup_match_bonus >= 0 AND setup_match_bonus <= 100),
  created_by_user_id                uuid REFERENCES users(id) ON DELETE RESTRICT,
  created_by                        text NOT NULL,
  created_at                        timestamptz NOT NULL DEFAULT now(),
  activated_by_user_id              uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by                      text,
  activated_at                      timestamptz,
  archived_by_user_id               uuid REFERENCES users(id) ON DELETE RESTRICT,
  archived_by                       text,
  archived_at                       timestamptz
);
CREATE UNIQUE INDEX dispatch_policy_single_active_idx ON dispatch_policy_versions((1)) WHERE status='ACTIVE';

INSERT INTO dispatch_policy_versions(
  id,version_no,status,freeze_minutes,firm_minutes,start_late_threshold_minutes,
  completion_late_threshold_minutes,auto_reschedule,min_auto_interval_minutes,
  setup_match_bonus,created_by,activated_by,activated_at
) VALUES (
  '00000000-0000-0000-0000-000000003901',1,'ACTIVE',240,1440,30,30,true,15,20,
  'SYSTEM','SYSTEM',now()
);

CREATE OR REPLACE VIEW v_current_dispatch_policy AS
SELECT * FROM dispatch_policy_versions WHERE status='ACTIVE';

CREATE TABLE schedule_adherence_snapshots (
  id                 uuid PRIMARY KEY,
  active_run_id      uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  policy_version_id  uuid NOT NULL REFERENCES dispatch_policy_versions(id) ON DELETE RESTRICT,
  as_of              timestamptz NOT NULL,
  status             text NOT NULL DEFAULT 'COMPLETE' CHECK (status='COMPLETE'),
  result_hash        text NOT NULL CHECK (length(result_hash)=64),
  generated_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  generated_by       text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedule_adherence_snapshots_run_idx ON schedule_adherence_snapshots(active_run_id,as_of DESC,id DESC);

CREATE TABLE schedule_adherence_rows (
  id                         uuid PRIMARY KEY,
  snapshot_id                uuid NOT NULL REFERENCES schedule_adherence_snapshots(id) ON DELETE RESTRICT,
  schedule_order_id          uuid NOT NULL REFERENCES detailed_schedule_orders(id) ON DELETE RESTRICT,
  work_order_id              uuid NOT NULL REFERENCES work_orders(id) ON DELETE RESTRICT,
  wo_operation_id            uuid NOT NULL REFERENCES wo_operations(id) ON DELETE RESTRICT,
  work_center_id             uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
  operation_seq              integer NOT NULL,
  planned_start              timestamptz,
  planned_end                timestamptz,
  actual_start               timestamptz,
  actual_end                 timestamptz,
  operation_status           text NOT NULL,
  start_variance_minutes     numeric NOT NULL DEFAULT 0,
  completion_variance_minutes numeric NOT NULL DEFAULT 0,
  start_on_time              boolean NOT NULL DEFAULT true,
  completion_on_time         boolean NOT NULL DEFAULT true,
  time_fence                 text NOT NULL CHECK (time_fence IN ('FROZEN','FIRM','FLEXIBLE','EXECUTED')),
  dispatch_status            text NOT NULL CHECK (dispatch_status IN ('IN_PROCESS','PAUSED','READY','QUEUED','BLOCKED','LATE_START','LATE_COMPLETE','COMPLETED')),
  blocked_reason             text NOT NULL DEFAULT '',
  UNIQUE(snapshot_id,wo_operation_id)
);
CREATE INDEX schedule_adherence_rows_snapshot_idx ON schedule_adherence_rows(snapshot_id,work_center_id,operation_seq);
CREATE INDEX schedule_adherence_rows_wo_idx ON schedule_adherence_rows(work_order_id,operation_seq,snapshot_id);

CREATE TABLE dynamic_reschedule_runs (
  id                    uuid PRIMARY KEY,
  source_run_id         uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  candidate_run_id      uuid REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  policy_version_id     uuid NOT NULL REFERENCES dispatch_policy_versions(id) ON DELETE RESTRICT,
  adherence_snapshot_id uuid REFERENCES schedule_adherence_snapshots(id) ON DELETE RESTRICT,
  trigger_type          text NOT NULL CHECK (trigger_type IN (
                          'MANUAL','SHOP_FLOOR_PROGRESS','LATE_OPERATION','BREAKDOWN','UNPLANNED_DOWNTIME',
                          'MAINTENANCE_CHANGE','CAPACITY_FEEDBACK_CHANGE','QUALITY_HOLD','MATERIAL_SHORTAGE','PRIORITY_CHANGE')),
  trigger_ref           text NOT NULL DEFAULT '',
  reason                text NOT NULL DEFAULT '',
  as_of                 timestamptz NOT NULL,
  freeze_until          timestamptz NOT NULL,
  firm_until            timestamptz NOT NULL,
  horizon_days          integer NOT NULL CHECK (horizon_days > 0 AND horizon_days <= 366),
  status                text NOT NULL CHECK (status IN ('EVALUATING','ACTIVATED','BLOCKED','NO_CHANGE','FAILED','THROTTLED')),
  frozen_conflicts      integer NOT NULL DEFAULT 0 CHECK (frozen_conflicts >= 0),
  execution_conflicts   integer NOT NULL DEFAULT 0 CHECK (execution_conflicts >= 0),
  firm_changes          integer NOT NULL DEFAULT 0 CHECK (firm_changes >= 0),
  flexible_changes      integer NOT NULL DEFAULT 0 CHECK (flexible_changes >= 0),
  impacted_work_orders  integer NOT NULL DEFAULT 0 CHECK (impacted_work_orders >= 0),
  result_hash           text CHECK (result_hash IS NULL OR length(result_hash)=64),
  actor_type            text NOT NULL CHECK (actor_type IN ('USER','SYSTEM')),
  actor_user_id         uuid REFERENCES users(id) ON DELETE RESTRICT,
  actor_username        text NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now(),
  finished_at           timestamptz,
  CHECK (firm_until >= freeze_until),
  CHECK ((actor_type='SYSTEM' AND actor_user_id IS NULL) OR (actor_type='USER' AND actor_user_id IS NOT NULL))
);
CREATE INDEX dynamic_reschedule_runs_status_idx ON dynamic_reschedule_runs(status,created_at DESC,id DESC);
CREATE INDEX dynamic_reschedule_runs_source_idx ON dynamic_reschedule_runs(source_run_id,created_at DESC,id DESC);

CREATE TABLE dynamic_reschedule_changes (
  id                    uuid PRIMARY KEY,
  reschedule_run_id     uuid NOT NULL REFERENCES dynamic_reschedule_runs(id) ON DELETE RESTRICT,
  work_order_id         uuid REFERENCES work_orders(id) ON DELETE RESTRICT,
  source_ref            text NOT NULL,
  operation_seq         integer NOT NULL,
  change_type           text NOT NULL CHECK (change_type IN ('ADDED','REMOVED','TIME_SHIFT','WORK_CENTER_CHANGE','TIME_AND_WORK_CENTER')),
  time_fence            text NOT NULL CHECK (time_fence IN ('FROZEN','FIRM','FLEXIBLE','EXECUTED')),
  old_work_center_id    uuid REFERENCES work_centers(id) ON DELETE RESTRICT,
  new_work_center_id    uuid REFERENCES work_centers(id) ON DELETE RESTRICT,
  old_start             timestamptz,
  old_end               timestamptz,
  new_start             timestamptz,
  new_end               timestamptz,
  start_shift_minutes   numeric NOT NULL DEFAULT 0,
  end_shift_minutes     numeric NOT NULL DEFAULT 0,
  frozen_conflict       boolean NOT NULL DEFAULT false,
  execution_conflict    boolean NOT NULL DEFAULT false,
  detail                jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dynamic_reschedule_changes_run_idx ON dynamic_reschedule_changes(reschedule_run_id,time_fence,source_ref,operation_seq);
CREATE INDEX dynamic_reschedule_changes_wo_idx ON dynamic_reschedule_changes(work_order_id,reschedule_run_id);

CREATE TABLE detailed_schedule_activation_history (
  id                    uuid PRIMARY KEY,
  previous_run_id       uuid REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  active_run_id         uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  reschedule_run_id     uuid REFERENCES dynamic_reschedule_runs(id) ON DELETE RESTRICT,
  policy_version_id     uuid NOT NULL REFERENCES dispatch_policy_versions(id) ON DELETE RESTRICT,
  activation_reason     text NOT NULL,
  actor_type            text NOT NULL CHECK (actor_type IN ('USER','SYSTEM')),
  actor_user_id         uuid REFERENCES users(id) ON DELETE RESTRICT,
  actor_username        text NOT NULL,
  activated_at          timestamptz NOT NULL DEFAULT now(),
  CHECK ((actor_type='SYSTEM' AND actor_user_id IS NULL) OR (actor_type='USER' AND actor_user_id IS NOT NULL))
);
CREATE INDEX detailed_schedule_activation_history_idx ON detailed_schedule_activation_history(activated_at DESC,id DESC);

CREATE TABLE detailed_schedule_execution_state (
  singleton             boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  active_run_id         uuid NOT NULL REFERENCES detailed_schedule_runs(id) ON DELETE RESTRICT,
  policy_version_id     uuid NOT NULL REFERENCES dispatch_policy_versions(id) ON DELETE RESTRICT,
  activation_history_id uuid NOT NULL REFERENCES detailed_schedule_activation_history(id) ON DELETE RESTRICT,
  activated_at          timestamptz NOT NULL,
  updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE schedule_reschedule_signals (
  id                    uuid PRIMARY KEY,
  trigger_type          text NOT NULL CHECK (trigger_type IN (
                          'SHOP_FLOOR_PROGRESS','LATE_OPERATION','BREAKDOWN','UNPLANNED_DOWNTIME',
                          'MAINTENANCE_CHANGE','CAPACITY_FEEDBACK_CHANGE','QUALITY_HOLD','MATERIAL_SHORTAGE','PRIORITY_CHANGE')),
  source_type           text NOT NULL,
  source_ref            text NOT NULL,
  work_center_id        uuid REFERENCES work_centers(id) ON DELETE RESTRICT,
  work_order_id         uuid REFERENCES work_orders(id) ON DELETE RESTRICT,
  detected_at           timestamptz NOT NULL DEFAULT now(),
  detail                jsonb NOT NULL DEFAULT '{}'::jsonb,
  processed_at          timestamptz,
  processed_run_id      uuid REFERENCES dynamic_reschedule_runs(id) ON DELETE RESTRICT
);
CREATE INDEX schedule_reschedule_signals_pending_idx ON schedule_reschedule_signals(detected_at,id) WHERE processed_at IS NULL;

CREATE OR REPLACE FUNCTION validate_dispatch_actor(p_user uuid,p_name text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE urole text; uname text; active boolean;
BEGIN
  SELECT role,username,is_active INTO urole,uname,active FROM users WHERE id=p_user;
  IF urole IS NULL OR NOT active OR urole NOT IN ('planner','admin') OR uname IS DISTINCT FROM p_name THEN
    RAISE EXCEPTION 'dispatch/rescheduling mutation requires matching active planner/admin actor' USING ERRCODE='42501';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION guard_dispatch_policy_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.created_by_user_id IS NULL THEN
      IF NEW.version_no<>1 OR NEW.created_by<>'SYSTEM' OR NEW.status<>'ACTIVE' THEN
        RAISE EXCEPTION 'only seeded dispatch policy may use SYSTEM actor' USING ERRCODE='23514';
      END IF;
    ELSE
      PERFORM validate_dispatch_actor(NEW.created_by_user_id,NEW.created_by);
      IF NEW.status<>'DRAFT' THEN
        RAISE EXCEPTION 'user-created dispatch policy must start DRAFT' USING ERRCODE='23514';
      END IF;
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.version_no IS DISTINCT FROM OLD.version_no OR
     NEW.freeze_minutes IS DISTINCT FROM OLD.freeze_minutes OR NEW.firm_minutes IS DISTINCT FROM OLD.firm_minutes OR
     NEW.start_late_threshold_minutes IS DISTINCT FROM OLD.start_late_threshold_minutes OR
     NEW.completion_late_threshold_minutes IS DISTINCT FROM OLD.completion_late_threshold_minutes OR
     NEW.auto_reschedule IS DISTINCT FROM OLD.auto_reschedule OR NEW.min_auto_interval_minutes IS DISTINCT FROM OLD.min_auto_interval_minutes OR
     NEW.setup_match_bonus IS DISTINCT FROM OLD.setup_match_bonus OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR
     NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'dispatch policy configuration/evidence is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status='ARCHIVED' THEN
    RAISE EXCEPTION 'archived dispatch policy is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status='ACTIVE' AND OLD.status<>'ACTIVE' THEN
    IF NEW.activated_by_user_id IS NULL OR NEW.activated_by IS NULL OR NEW.activated_at IS NULL THEN
      RAISE EXCEPTION 'dispatch policy activation audit required' USING ERRCODE='23514';
    END IF;
    PERFORM validate_dispatch_actor(NEW.activated_by_user_id,NEW.activated_by);
  END IF;
  IF NEW.status='ARCHIVED' AND OLD.status<>'ARCHIVED' THEN
    IF NEW.archived_by_user_id IS NULL OR NEW.archived_by IS NULL OR NEW.archived_at IS NULL THEN
      RAISE EXCEPTION 'dispatch policy archive audit required' USING ERRCODE='23514';
    END IF;
    PERFORM validate_dispatch_actor(NEW.archived_by_user_id,NEW.archived_by);
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER dispatch_policy_guard_trg BEFORE INSERT OR UPDATE ON dispatch_policy_versions
FOR EACH ROW EXECUTE FUNCTION guard_dispatch_policy_version();

CREATE OR REPLACE FUNCTION reject_dispatch_policy_delete()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'dispatch policy versions are audit evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER dispatch_policy_no_delete_trg BEFORE DELETE ON dispatch_policy_versions
FOR EACH ROW EXECUTE FUNCTION reject_dispatch_policy_delete();

CREATE OR REPLACE FUNCTION reject_schedule_execution_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'schedule execution evidence is append-only' USING ERRCODE='23514';
END$$;
CREATE TRIGGER adherence_snapshot_immutable_trg BEFORE UPDATE OR DELETE ON schedule_adherence_snapshots
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();
CREATE TRIGGER adherence_rows_immutable_trg BEFORE UPDATE OR DELETE ON schedule_adherence_rows
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();
CREATE TRIGGER reschedule_changes_immutable_trg BEFORE UPDATE OR DELETE ON dynamic_reschedule_changes
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();
CREATE TRIGGER activation_history_immutable_trg BEFORE UPDATE OR DELETE ON detailed_schedule_activation_history
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();

CREATE OR REPLACE FUNCTION guard_dynamic_reschedule_run()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.actor_type='USER' THEN
      PERFORM validate_dispatch_actor(NEW.actor_user_id,NEW.actor_username);
    ELSIF NEW.actor_username<>'SYSTEM' THEN
      RAISE EXCEPTION 'SYSTEM reschedule actor must be named SYSTEM' USING ERRCODE='23514';
    END IF;
    IF NEW.status<>'EVALUATING' AND NEW.status<>'THROTTLED' THEN
      RAISE EXCEPTION 'reschedule run must begin EVALUATING or THROTTLED' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.status<>'EVALUATING' THEN
    RAISE EXCEPTION 'completed reschedule run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.source_run_id IS DISTINCT FROM OLD.source_run_id OR
     NEW.policy_version_id IS DISTINCT FROM OLD.policy_version_id OR NEW.trigger_type IS DISTINCT FROM OLD.trigger_type OR
     NEW.trigger_ref IS DISTINCT FROM OLD.trigger_ref OR NEW.reason IS DISTINCT FROM OLD.reason OR NEW.as_of IS DISTINCT FROM OLD.as_of OR
     NEW.freeze_until IS DISTINCT FROM OLD.freeze_until OR NEW.firm_until IS DISTINCT FROM OLD.firm_until OR NEW.horizon_days IS DISTINCT FROM OLD.horizon_days OR
     NEW.actor_type IS DISTINCT FROM OLD.actor_type OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id OR NEW.actor_username IS DISTINCT FROM OLD.actor_username OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'reschedule input evidence is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('ACTIVATED','BLOCKED','NO_CHANGE','FAILED') THEN
    RAISE EXCEPTION 'invalid terminal reschedule status' USING ERRCODE='23514';
  END IF;
  IF NEW.finished_at IS NULL THEN
    RAISE EXCEPTION 'terminal reschedule run requires finished_at' USING ERRCODE='23514';
  END IF;
  IF NEW.status IN ('ACTIVATED','BLOCKED','NO_CHANGE') AND (NEW.candidate_run_id IS NULL OR NEW.result_hash IS NULL) THEN
    RAISE EXCEPTION 'evaluated reschedule requires candidate and canonical result hash' USING ERRCODE='23514';
  END IF;
  IF NEW.status='ACTIVATED' AND (NEW.frozen_conflicts<>0 OR NEW.execution_conflicts<>0) THEN
    RAISE EXCEPTION 'reschedule with frozen or executed commitment conflicts cannot activate' USING ERRCODE='23514';
  END IF;
  IF NEW.status='BLOCKED' AND NEW.frozen_conflicts=0 AND NEW.execution_conflicts=0 THEN
    RAISE EXCEPTION 'blocked reschedule requires frozen or executed commitment conflict evidence' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER dynamic_reschedule_run_guard_trg BEFORE INSERT OR UPDATE ON dynamic_reschedule_runs
FOR EACH ROW EXECUTE FUNCTION guard_dynamic_reschedule_run();

CREATE OR REPLACE FUNCTION reject_dynamic_reschedule_run_delete()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
  RAISE EXCEPTION 'reschedule runs are immutable audit evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER dynamic_reschedule_run_no_delete_trg BEFORE DELETE ON dynamic_reschedule_runs
FOR EACH ROW EXECUTE FUNCTION reject_dynamic_reschedule_run_delete();

CREATE OR REPLACE FUNCTION guard_activation_history_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE run_status text; policy_status text;
BEGIN
  SELECT status INTO run_status FROM detailed_schedule_runs WHERE id=NEW.active_run_id;
  IF run_status IS DISTINCT FROM 'COMPLETE' THEN
    RAISE EXCEPTION 'only COMPLETE detailed schedule may become execution schedule' USING ERRCODE='23514';
  END IF;
  SELECT status INTO policy_status FROM dispatch_policy_versions WHERE id=NEW.policy_version_id;
  IF policy_status IS DISTINCT FROM 'ACTIVE' THEN
    RAISE EXCEPTION 'execution schedule requires ACTIVE dispatch policy' USING ERRCODE='23514';
  END IF;
  IF NEW.reschedule_run_id IS NOT NULL THEN
    IF NOT EXISTS (SELECT 1 FROM dynamic_reschedule_runs r WHERE r.id=NEW.reschedule_run_id AND r.status='ACTIVATED' AND r.candidate_run_id=NEW.active_run_id) THEN
      RAISE EXCEPTION 'reschedule activation must reference its evaluated candidate' USING ERRCODE='23514';
    END IF;
  END IF;
  IF NEW.actor_type='USER' THEN
    PERFORM validate_dispatch_actor(NEW.actor_user_id,NEW.actor_username);
  ELSIF NEW.actor_username<>'SYSTEM' THEN
    RAISE EXCEPTION 'SYSTEM activation actor must be named SYSTEM' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER activation_history_insert_guard_trg BEFORE INSERT ON detailed_schedule_activation_history
FOR EACH ROW EXECUTE FUNCTION guard_activation_history_insert();

CREATE OR REPLACE FUNCTION guard_execution_state()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE hrun uuid; hpolicy uuid; hat timestamptz;
BEGIN
  SELECT active_run_id,policy_version_id,activated_at INTO hrun,hpolicy,hat
    FROM detailed_schedule_activation_history WHERE id=NEW.activation_history_id;
  IF hrun IS DISTINCT FROM NEW.active_run_id OR hpolicy IS DISTINCT FROM NEW.policy_version_id OR hat IS DISTINCT FROM NEW.activated_at THEN
    RAISE EXCEPTION 'execution state must exactly match activation history' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER execution_state_guard_trg BEFORE INSERT OR UPDATE ON detailed_schedule_execution_state
FOR EACH ROW EXECUTE FUNCTION guard_execution_state();
CREATE TRIGGER execution_state_no_delete_trg BEFORE DELETE ON detailed_schedule_execution_state
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();

-- Signals are append-only except for the one-time processing acknowledgement.
CREATE OR REPLACE FUNCTION guard_reschedule_signal_update()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.trigger_type IS DISTINCT FROM OLD.trigger_type OR NEW.source_type IS DISTINCT FROM OLD.source_type OR
     NEW.source_ref IS DISTINCT FROM OLD.source_ref OR NEW.work_center_id IS DISTINCT FROM OLD.work_center_id OR NEW.work_order_id IS DISTINCT FROM OLD.work_order_id OR
     NEW.detected_at IS DISTINCT FROM OLD.detected_at OR NEW.detail IS DISTINCT FROM OLD.detail THEN
    RAISE EXCEPTION 'reschedule signal evidence is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.processed_at IS NOT NULL THEN
    RAISE EXCEPTION 'processed reschedule signal is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.processed_at IS NULL OR NEW.processed_run_id IS NULL THEN
    RAISE EXCEPTION 'signal processing requires timestamp and reschedule run' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER reschedule_signal_update_guard_trg BEFORE UPDATE ON schedule_reschedule_signals
FOR EACH ROW EXECUTE FUNCTION guard_reschedule_signal_update();
CREATE TRIGGER reschedule_signal_no_delete_trg BEFORE DELETE ON schedule_reschedule_signals
FOR EACH ROW EXECUTE FUNCTION reject_schedule_execution_evidence_mutation();

-- Shop Floor progress automatically queues an execution-planning signal.
CREATE OR REPLACE FUNCTION enqueue_shopfloor_reschedule_signal()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wid uuid; wcid uuid;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM detailed_schedule_execution_state WHERE singleton=true) THEN
    RETURN NEW;
  END IF;
  SELECT wo_id,work_center_id INTO wid,wcid FROM wo_operations WHERE id=NEW.wo_op_id;
  INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,work_center_id,work_order_id,detected_at,detail)
  VALUES(gen_random_uuid(),CASE WHEN NEW.event_type='STOP' THEN 'LATE_OPERATION' ELSE 'SHOP_FLOOR_PROGRESS' END,
         'OPERATION_LOG',NEW.id::text,wcid,wid,NEW.event_at,
         jsonb_build_object('eventType',NEW.event_type,'operationId',NEW.wo_op_id,'quantity',NEW.quantity));
  RETURN NEW;
END$$;
CREATE TRIGGER operation_log_reschedule_signal_trg
AFTER INSERT ON operation_logs FOR EACH ROW
WHEN (NEW.event_type IN ('START','STOP','COMPLETE','SCRAP'))
EXECUTE FUNCTION enqueue_shopfloor_reschedule_signal();

-- Maintenance revisions queue capacity-change signals without coupling the DB to the scheduler.
CREATE OR REPLACE FUNCTION enqueue_maintenance_reschedule_signal()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE typ text; wcid uuid;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM detailed_schedule_execution_state WHERE singleton=true) THEN
    RETURN NEW;
  END IF;
  SELECT event_type,work_center_id INTO typ,wcid FROM maintenance_events WHERE id=NEW.maintenance_event_id;
  INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,work_center_id,detected_at,detail)
  VALUES(gen_random_uuid(),CASE WHEN typ='BREAKDOWN' THEN 'BREAKDOWN' WHEN typ='UNPLANNED_DOWNTIME' THEN 'UNPLANNED_DOWNTIME' ELSE 'MAINTENANCE_CHANGE' END,
         'MAINTENANCE_REVISION',NEW.id::text,wcid,NEW.occurred_at,
         jsonb_build_object('maintenanceEventId',NEW.maintenance_event_id,'revisionNo',NEW.revision_no,'eventType',typ,'status',NEW.status));
  RETURN NEW;
END$$;
CREATE TRIGGER maintenance_reschedule_signal_trg
AFTER INSERT ON maintenance_event_revisions FOR EACH ROW EXECUTE FUNCTION enqueue_maintenance_reschedule_signal();

CREATE OR REPLACE FUNCTION enqueue_capacity_feedback_reschedule_signal()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM detailed_schedule_execution_state WHERE singleton=true) THEN
    RETURN NEW;
  END IF;
  IF NEW.status='ACTIVE' AND OLD.status IS DISTINCT FROM 'ACTIVE' THEN
    INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,work_center_id,detected_at,detail)
    VALUES(gen_random_uuid(),'CAPACITY_FEEDBACK_CHANGE','CAPACITY_FEEDBACK',NEW.id::text,NEW.work_center_id,COALESCE(NEW.activated_at,now()),
           jsonb_build_object('versionNo',NEW.version_no,'effectiveEfficiency',NEW.effective_efficiency,'effectiveUtilization',NEW.effective_utilization));
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER capacity_feedback_reschedule_signal_trg
AFTER UPDATE ON capacity_feedback_versions FOR EACH ROW EXECUTE FUNCTION enqueue_capacity_feedback_reschedule_signal();

-- Quality HOLD is already DB-controlled and audit-backed by the 0024 quality transaction history.
-- Queue a global material/quality re-evaluation only when an execution schedule exists.
CREATE OR REPLACE FUNCTION enqueue_quality_hold_reschedule_signal()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM detailed_schedule_execution_state WHERE singleton=true) THEN
    RETURN NEW;
  END IF;
  INSERT INTO schedule_reschedule_signals(id,trigger_type,source_type,source_ref,detected_at,detail)
  VALUES(gen_random_uuid(),'QUALITY_HOLD','LOT_QUALITY',NEW.id::text,now(),
         jsonb_build_object('lotId',NEW.id,'fromQualityStatus',OLD.quality_status,'toQualityStatus',NEW.quality_status));
  RETURN NEW;
END$$;
CREATE TRIGGER lot_quality_hold_reschedule_signal_trg
AFTER UPDATE OF quality_status ON lots FOR EACH ROW
WHEN (NEW.quality_status='HOLD' AND OLD.quality_status IS DISTINCT FROM NEW.quality_status)
EXECUTE FUNCTION enqueue_quality_hold_reschedule_signal();

-- Extend Full Pegging vocabulary with schedule-execution/root-cause evidence.
ALTER TABLE pegging_nodes DROP CONSTRAINT IF EXISTS pegging_nodes_node_type_check;
ALTER TABLE pegging_nodes ADD CONSTRAINT pegging_nodes_node_type_check CHECK (node_type IN (
  'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
  'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
  'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE',
  'SUPPLIER_CONFIRMATION','SUPPLIER_ASN','LEAD_TIME_PROFILE','INVENTORY_POLICY','MAINTENANCE_EVENT','CAPACITY_FEEDBACK','RESCHEDULE_RUN'
));

ALTER TABLE pegging_edges DROP CONSTRAINT IF EXISTS pegging_edges_edge_type_check;
ALTER TABLE pegging_edges ADD CONSTRAINT pegging_edges_edge_type_check CHECK (edge_type IN (
  'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
  'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
  'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY',
  'CONFIRMED_BY','SHIPPED_BY','PLANNED_USING','PROTECTED_BY','CAPACITY_REDUCED_BY','CALIBRATED_BY','RESCHEDULED_BY'
));

ALTER TABLE planning_exceptions DROP CONSTRAINT IF EXISTS planning_exceptions_exception_type_check;
ALTER TABLE planning_exceptions ADD CONSTRAINT planning_exceptions_exception_type_check CHECK (exception_type IN (
  'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
  'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
  'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED',
  'SUPPLIER_CONFIRMATION_LATE','SUPPLIER_RELIABILITY_RISK',
  'SAFETY_STOCK_BREACH','REORDER_POINT_BREACH',
  'PREVENTIVE_MAINTENANCE_CAPACITY','BREAKDOWN_CAPACITY',
  'PLANNED_DOWNTIME_CAPACITY','UNPLANNED_DOWNTIME_CAPACITY','OEE_CAPACITY_RISK',
  'SCHEDULE_START_LATE','SCHEDULE_COMPLETION_LATE','DISPATCH_BLOCKED','RESCHEDULE_REQUIRED',
  'FROZEN_HORIZON_CONFLICT','EXECUTION_COMMITMENT_CONFLICT','FIRM_HORIZON_CHANGE'
));
