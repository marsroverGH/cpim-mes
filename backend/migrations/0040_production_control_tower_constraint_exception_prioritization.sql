-- 0040: Production Control Tower + Constraint / Exception Prioritization
--
-- Builds an intervention-oriented control layer on top of immutable Full
-- Pegging / Planning Exception evidence. Existing planning_exceptions remain
-- immutable. Control Tower cases provide stable operational identity while
-- snapshots, recommendations and actions remain append-only audit evidence.
--
-- The Control Tower answers:
--   * Which customer/order problem requires intervention first?
--   * What is the business impact?
--   * What constraint/root cause is responsible?
--   * What intervention is recommended?
--   * Who owns the case and what was done?

CREATE TABLE control_tower_cases (
  id                    uuid PRIMARY KEY,
  case_key              text NOT NULL UNIQUE,
  sales_order_id        uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  sales_order_line_id   uuid REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  exception_type        text NOT NULL,
  first_exception_id    uuid NOT NULL REFERENCES planning_exceptions(id) ON DELETE RESTRICT,
  first_detected_at     timestamptz NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX control_tower_cases_order_idx
  ON control_tower_cases(sales_order_id, first_detected_at DESC, id);

CREATE INDEX control_tower_cases_line_idx
  ON control_tower_cases(sales_order_line_id, first_detected_at DESC, id)
  WHERE sales_order_line_id IS NOT NULL;

CREATE INDEX control_tower_cases_type_idx
  ON control_tower_cases(exception_type, first_detected_at DESC, id);


-- Immutable evaluation of one Control Tower case at a specific point in time.
CREATE TABLE control_tower_case_snapshots (
  id                    uuid PRIMARY KEY,
  case_id               uuid NOT NULL REFERENCES control_tower_cases(id) ON DELETE RESTRICT,
  planning_exception_id uuid NOT NULL REFERENCES planning_exceptions(id) ON DELETE RESTRICT,
  pegging_run_id        uuid NOT NULL REFERENCES pegging_runs(id) ON DELETE RESTRICT,
  as_of                 timestamptz NOT NULL,

  severity              text NOT NULL
                        CHECK (severity IN ('INFO','WARNING','CRITICAL')),
  impact_days           integer NOT NULL DEFAULT 0
                        CHECK (impact_days >= 0),

  order_value           numeric(18,2) NOT NULL DEFAULT 0
                        CHECK (order_value >= 0),
  open_order_value      numeric(18,2) NOT NULL DEFAULT 0
                        CHECK (open_order_value >= 0),

  order_priority        text NOT NULL
                        CHECK (order_priority IN ('EXPEDITE','HIGH','NORMAL')),
  service_class_code    text NOT NULL
                        REFERENCES customer_service_classes(code) ON DELETE RESTRICT,

  revenue_at_risk       numeric(18,2) NOT NULL DEFAULT 0
                        CHECK (revenue_at_risk >= 0),

  severity_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (severity_score BETWEEN 0 AND 100),
  lateness_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (lateness_score BETWEEN 0 AND 100),
  revenue_score         numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (revenue_score BETWEEN 0 AND 100),
  customer_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (customer_score BETWEEN 0 AND 100),
  material_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (material_score BETWEEN 0 AND 100),
  capacity_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (capacity_score BETWEEN 0 AND 100),
  supplier_score        numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (supplier_score BETWEEN 0 AND 100),
  execution_score       numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (execution_score BETWEEN 0 AND 100),
  aging_score           numeric(8,3) NOT NULL DEFAULT 0
                        CHECK (aging_score BETWEEN 0 AND 100),

  priority_score        numeric(8,3) NOT NULL
                        CHECK (priority_score BETWEEN 0 AND 100),
  priority_band         text NOT NULL
                        CHECK (priority_band IN ('P1','P2','P3','P4')),

  root_cause_type       text NOT NULL DEFAULT '',
  root_cause_ref        text NOT NULL DEFAULT '',

  result_hash           text NOT NULL CHECK (length(result_hash)=64),
  created_at            timestamptz NOT NULL DEFAULT now(),

  UNIQUE(case_id,result_hash)
);

CREATE INDEX control_tower_snapshots_case_idx
  ON control_tower_case_snapshots(case_id,as_of DESC,created_at DESC,id DESC);

CREATE INDEX control_tower_snapshots_priority_idx
  ON control_tower_case_snapshots(priority_band,priority_score DESC,as_of DESC,id DESC);

CREATE INDEX control_tower_snapshots_exception_idx
  ON control_tower_case_snapshots(planning_exception_id,as_of DESC,id DESC);

CREATE INDEX control_tower_snapshots_run_idx
  ON control_tower_case_snapshots(pegging_run_id,as_of DESC,id DESC);


-- Ranked intervention recommendations belonging to an immutable snapshot.
CREATE TABLE control_tower_recommendations (
  id                    uuid PRIMARY KEY,
  snapshot_id           uuid NOT NULL
                        REFERENCES control_tower_case_snapshots(id) ON DELETE RESTRICT,
  rank_no               integer NOT NULL CHECK (rank_no > 0),

  action_type           text NOT NULL CHECK (action_type IN (
                          'EXPEDITE_PO',
                          'RESCHEDULE_WO',
                          'ALTERNATE_WORK_CENTER',
                          'RELEASE_WO',
                          'REVIEW_CAPACITY',
                          'REVIEW_QUALITY_HOLD',
                          'RECALCULATE_PROMISE',
                          'CONTACT_CUSTOMER',
                          'REVIEW_FROZEN_CONFLICT',
                          'MANUAL_REVIEW'
                        )),

  target_type           text NOT NULL DEFAULT '',
  target_ref            text NOT NULL DEFAULT '',
  title                 text NOT NULL,
  reason                text NOT NULL DEFAULT '',
  estimated_effect      jsonb NOT NULL DEFAULT '{}'::jsonb,
  requires_approval     boolean NOT NULL DEFAULT false,
  created_at            timestamptz NOT NULL DEFAULT now(),

  UNIQUE(snapshot_id,rank_no)
);

CREATE INDEX control_tower_recommendations_snapshot_idx
  ON control_tower_recommendations(snapshot_id,rank_no,id);


-- Append-only lifecycle / assignment evidence.
CREATE TABLE control_tower_case_actions (
  id                    uuid PRIMARY KEY,
  case_id               uuid NOT NULL
                        REFERENCES control_tower_cases(id) ON DELETE RESTRICT,

  action_type           text NOT NULL CHECK (action_type IN (
                          'ACKNOWLEDGE',
                          'ASSIGN',
                          'START',
                          'RESOLVE',
                          'REOPEN',
                          'CLOSE'
                        )),

  from_status           text NOT NULL DEFAULT 'OPEN'
                        CHECK (from_status IN (
                          'OPEN',
                          'ACKNOWLEDGED',
                          'ASSIGNED',
                          'IN_PROGRESS',
                          'RESOLVED',
                          'CLOSED'
                        )),

  to_status             text NOT NULL DEFAULT 'OPEN'
                        CHECK (to_status IN (
                          'OPEN',
                          'ACKNOWLEDGED',
                          'ASSIGNED',
                          'IN_PROGRESS',
                          'RESOLVED',
                          'CLOSED'
                        )),

  assigned_to_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  assigned_to_username  text,

  actor_user_id         uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  actor_username        text NOT NULL,
  comment               text NOT NULL DEFAULT '',
  occurred_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX control_tower_case_actions_case_idx
  ON control_tower_case_actions(case_id,occurred_at,id);

CREATE INDEX control_tower_case_actions_assignee_idx
  ON control_tower_case_actions(assigned_to_user_id,occurred_at DESC,id DESC)
  WHERE assigned_to_user_id IS NOT NULL;


-- Stable case identity must match the immutable Planning Exception that
-- originally opened it.
CREATE OR REPLACE FUNCTION guard_control_tower_case_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  e_order uuid;
  e_line uuid;
  e_type text;
  e_detected timestamptz;
BEGIN
  SELECT sales_order_id,sales_order_line_id,exception_type,detected_at
    INTO e_order,e_line,e_type,e_detected
    FROM planning_exceptions
   WHERE id=NEW.first_exception_id;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'control tower first planning exception % does not exist',
      NEW.first_exception_id USING ERRCODE='23503';
  END IF;

  IF e_order IS DISTINCT FROM NEW.sales_order_id OR
     e_line IS DISTINCT FROM NEW.sales_order_line_id OR
     e_type IS DISTINCT FROM NEW.exception_type THEN
    RAISE EXCEPTION
      'control tower case identity does not match first planning exception'
      USING ERRCODE='23514';
  END IF;

  IF NEW.sales_order_line_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
      FROM sales_order_lines l
     WHERE l.id=NEW.sales_order_line_id
       AND l.sales_order_id=NEW.sales_order_id
  ) THEN
    RAISE EXCEPTION
      'control tower Sales Order line does not belong to Sales Order'
      USING ERRCODE='23514';
  END IF;

  NEW.first_detected_at := e_detected;
  NEW.created_at := transaction_timestamp();
  RETURN NEW;
END$$;

CREATE TRIGGER control_tower_case_insert_guard_trg
BEFORE INSERT ON control_tower_cases
FOR EACH ROW EXECUTE FUNCTION guard_control_tower_case_insert();


-- Snapshot must point to the same case/order/line/exception type and must use
-- an exception that is operationally current at snapshot time.
CREATE OR REPLACE FUNCTION guard_control_tower_snapshot_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  c_order uuid;
  c_line uuid;
  c_type text;
  e_order uuid;
  e_line uuid;
  e_type text;
  e_run uuid;
BEGIN
  SELECT sales_order_id,sales_order_line_id,exception_type
    INTO c_order,c_line,c_type
    FROM control_tower_cases
   WHERE id=NEW.case_id;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'control tower case % does not exist',NEW.case_id
      USING ERRCODE='23503';
  END IF;

  SELECT sales_order_id,sales_order_line_id,exception_type,run_id
    INTO e_order,e_line,e_type,e_run
    FROM planning_exceptions
   WHERE id=NEW.planning_exception_id;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'planning exception % does not exist',
      NEW.planning_exception_id USING ERRCODE='23503';
  END IF;

  IF c_order IS DISTINCT FROM e_order OR
     c_line IS DISTINCT FROM e_line OR
     c_type IS DISTINCT FROM e_type THEN
    RAISE EXCEPTION
      'control tower snapshot planning exception does not match case identity'
      USING ERRCODE='23514';
  END IF;

  IF e_run IS DISTINCT FROM NEW.pegging_run_id THEN
    RAISE EXCEPTION
      'control tower snapshot pegging run does not match planning exception'
      USING ERRCODE='23514';
  END IF;

  IF NOT EXISTS (
    SELECT 1
      FROM v_current_planning_exceptions x
     WHERE x.id=NEW.planning_exception_id
  ) THEN
    RAISE EXCEPTION
      'control tower snapshot requires an operationally current planning exception'
      USING ERRCODE='23514';
  END IF;

  NEW.created_at := transaction_timestamp();
  RETURN NEW;
END$$;

CREATE TRIGGER control_tower_snapshot_insert_guard_trg
BEFORE INSERT ON control_tower_case_snapshots
FOR EACH ROW EXECUTE FUNCTION guard_control_tower_snapshot_insert();


-- Control Tower analytical and lifecycle evidence is immutable.
CREATE OR REPLACE FUNCTION reject_control_tower_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is immutable Control Tower evidence',TG_TABLE_NAME
    USING ERRCODE='23514';
END$$;

CREATE TRIGGER control_tower_cases_immutable_trg
BEFORE UPDATE OR DELETE ON control_tower_cases
FOR EACH ROW EXECUTE FUNCTION reject_control_tower_evidence_mutation();

CREATE TRIGGER control_tower_snapshots_immutable_trg
BEFORE UPDATE OR DELETE ON control_tower_case_snapshots
FOR EACH ROW EXECUTE FUNCTION reject_control_tower_evidence_mutation();

CREATE TRIGGER control_tower_recommendations_immutable_trg
BEFORE UPDATE OR DELETE ON control_tower_recommendations
FOR EACH ROW EXECUTE FUNCTION reject_control_tower_evidence_mutation();

CREATE TRIGGER control_tower_actions_immutable_trg
BEFORE UPDATE OR DELETE ON control_tower_case_actions
FOR EACH ROW EXECUTE FUNCTION reject_control_tower_evidence_mutation();


CREATE OR REPLACE FUNCTION control_tower_case_current_status(p_case uuid)
RETURNS text LANGUAGE sql STABLE AS $$
  SELECT COALESCE((
    SELECT a.to_status
      FROM control_tower_case_actions a
     WHERE a.case_id=p_case
     ORDER BY a.occurred_at DESC,a.id DESC
     LIMIT 1
  ),'OPEN')
$$;


-- Only planner/admin may mutate Control Tower workflow.
-- Assignment may target any active application user.
CREATE OR REPLACE FUNCTION guard_control_tower_case_action()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  cur text;
  actor_name text;
  actor_active boolean;
  actor_role text;
  assignee_name text;
  assignee_active boolean;
BEGIN
  PERFORM pg_advisory_xact_lock(
    hashtextextended('cpim:control-tower-case:'||NEW.case_id::text,0)
  );

  IF NOT EXISTS (
    SELECT 1 FROM control_tower_cases WHERE id=NEW.case_id
  ) THEN
    RAISE EXCEPTION 'control tower case % does not exist',NEW.case_id
      USING ERRCODE='23503';
  END IF;

  SELECT username,is_active,role
    INTO actor_name,actor_active,actor_role
    FROM users
   WHERE id=NEW.actor_user_id;

  IF NOT FOUND OR NOT actor_active OR
     actor_name IS DISTINCT FROM NEW.actor_username THEN
    RAISE EXCEPTION
      'control tower action actor must be an active matching user'
      USING ERRCODE='42501';
  END IF;

  IF actor_role NOT IN ('admin','planner') THEN
    RAISE EXCEPTION
      'control tower actions require planner/admin actor'
      USING ERRCODE='42501';
  END IF;

  cur := control_tower_case_current_status(NEW.case_id);
  NEW.from_status := cur;

  IF NEW.action_type='ACKNOWLEDGE' THEN
    IF cur<>'OPEN' THEN
      RAISE EXCEPTION 'ACKNOWLEDGE requires OPEN status'
        USING ERRCODE='23514';
    END IF;
    NEW.to_status := 'ACKNOWLEDGED';

  ELSIF NEW.action_type='ASSIGN' THEN
    IF cur NOT IN ('OPEN','ACKNOWLEDGED','ASSIGNED','IN_PROGRESS') THEN
      RAISE EXCEPTION 'ASSIGN is not valid while case status is %',cur
        USING ERRCODE='23514';
    END IF;

    IF NEW.assigned_to_user_id IS NULL THEN
      RAISE EXCEPTION 'ASSIGN requires assigned_to_user_id'
        USING ERRCODE='23514';
    END IF;

    SELECT username,is_active
      INTO assignee_name,assignee_active
      FROM users
     WHERE id=NEW.assigned_to_user_id;

    IF NOT FOUND OR NOT assignee_active THEN
      RAISE EXCEPTION 'Control Tower assignee must be an active user'
        USING ERRCODE='23514';
    END IF;

    NEW.assigned_to_username := assignee_name;

    IF cur='IN_PROGRESS' THEN
      NEW.to_status := 'IN_PROGRESS';
    ELSE
      NEW.to_status := 'ASSIGNED';
    END IF;

  ELSIF NEW.action_type='START' THEN
    IF cur NOT IN ('ACKNOWLEDGED','ASSIGNED') THEN
      RAISE EXCEPTION 'START requires ACKNOWLEDGED or ASSIGNED status'
        USING ERRCODE='23514';
    END IF;
    NEW.to_status := 'IN_PROGRESS';

  ELSIF NEW.action_type='RESOLVE' THEN
    IF cur NOT IN ('OPEN','ACKNOWLEDGED','ASSIGNED','IN_PROGRESS') THEN
      RAISE EXCEPTION 'RESOLVE is not valid while case status is %',cur
        USING ERRCODE='23514';
    END IF;
    NEW.to_status := 'RESOLVED';

  ELSIF NEW.action_type='REOPEN' THEN
    IF cur<>'RESOLVED' THEN
      RAISE EXCEPTION 'REOPEN requires RESOLVED status'
        USING ERRCODE='23514';
    END IF;
    NEW.to_status := 'OPEN';

  ELSIF NEW.action_type='CLOSE' THEN
    IF cur<>'RESOLVED' THEN
      RAISE EXCEPTION 'CLOSE requires RESOLVED status'
        USING ERRCODE='23514';
    END IF;
    NEW.to_status := 'CLOSED';

  ELSE
    RAISE EXCEPTION 'invalid Control Tower action type'
      USING ERRCODE='23514';
  END IF;

  IF NEW.action_type<>'ASSIGN' THEN
    NEW.assigned_to_user_id := NULL;
    NEW.assigned_to_username := NULL;
  END IF;

  NEW.occurred_at := transaction_timestamp();
  RETURN NEW;
END$$;

CREATE TRIGGER control_tower_case_action_guard_trg
BEFORE INSERT ON control_tower_case_actions
FOR EACH ROW EXECUTE FUNCTION guard_control_tower_case_action();


-- Current operational Control Tower view.
-- Case identity/history remains immutable; latest analytical snapshot and
-- workflow state are projected here.
CREATE OR REPLACE VIEW v_current_control_tower_cases AS
WITH latest_snapshot AS (
  SELECT DISTINCT ON (case_id) *
    FROM control_tower_case_snapshots
   ORDER BY case_id,as_of DESC,created_at DESC,id DESC
),
latest_action AS (
  SELECT DISTINCT ON (case_id) *
    FROM control_tower_case_actions
   ORDER BY case_id,occurred_at DESC,id DESC
),
latest_assignment AS (
  SELECT DISTINCT ON (case_id)
         case_id,assigned_to_user_id,assigned_to_username,occurred_at
    FROM control_tower_case_actions
   WHERE action_type='ASSIGN'
     AND assigned_to_user_id IS NOT NULL
   ORDER BY case_id,occurred_at DESC,id DESC
)
SELECT
  c.id AS case_id,
  c.case_key,
  c.sales_order_id,
  c.sales_order_line_id,
  c.exception_type,
  c.first_exception_id,
  c.first_detected_at,
  c.created_at AS case_created_at,

  control_tower_case_current_status(c.id) AS current_status,

  la.action_type AS latest_action_type,
  la.actor_username AS latest_actor,
  la.comment AS latest_comment,
  la.occurred_at AS latest_action_at,

  own.assigned_to_user_id AS owner_user_id,
  own.assigned_to_username AS owner_username,

  s.id AS snapshot_id,
  s.planning_exception_id,
  s.pegging_run_id,
  s.as_of,
  s.severity,
  s.impact_days,
  s.order_value,
  s.open_order_value,
  s.order_priority,
  s.service_class_code,
  s.revenue_at_risk,

  s.severity_score,
  s.lateness_score,
  s.revenue_score,
  s.customer_score,
  s.material_score,
  s.capacity_score,
  s.supplier_score,
  s.execution_score,
  s.aging_score,

  s.priority_score,
  s.priority_band,

  s.root_cause_type,
  s.root_cause_ref,
  s.result_hash,
  s.created_at AS snapshot_created_at,

  so.order_no AS sales_order_no,
  cu.customer_no,
  cu.name AS customer_name,
  l.line_no,
  i.code AS item_code,
  i.name AS item_name

FROM control_tower_cases c
JOIN sales_orders so
  ON so.id=c.sales_order_id
JOIN customers cu
  ON cu.id=so.customer_id
LEFT JOIN sales_order_lines l
  ON l.id=c.sales_order_line_id
LEFT JOIN items i
  ON i.id=l.item_id
LEFT JOIN latest_snapshot s
  ON s.case_id=c.id
LEFT JOIN latest_action la
  ON la.case_id=c.id
LEFT JOIN latest_assignment own
  ON own.case_id=c.id;


COMMENT ON TABLE control_tower_cases IS
  'Stable operational identity for a Production Control Tower intervention case';

COMMENT ON TABLE control_tower_case_snapshots IS
  'Immutable business-impact and priority evaluation evidence for a Control Tower case';

COMMENT ON TABLE control_tower_recommendations IS
  'Immutable ranked intervention recommendations generated from a Control Tower snapshot';

COMMENT ON TABLE control_tower_case_actions IS
  'Append-only Control Tower acknowledgement, assignment and resolution workflow evidence';

COMMENT ON VIEW v_current_control_tower_cases IS
  'Current Production Control Tower projection using latest immutable priority snapshot and workflow evidence';
