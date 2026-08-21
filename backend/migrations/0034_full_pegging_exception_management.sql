-- ============================================================================
-- 0034: Full Pegging + Exception Management
--
-- Creates immutable, auditable supply/demand pegging snapshots that trace a
-- Sales Order through inventory, accepted promise/BOP decisions, Work Orders,
-- BOM/component supply, Purchase Orders, supplier/quality constraints and the
-- latest Detailed Scheduling capacity evidence. Planning exceptions are
-- immutable detections; operator decisions are append-only actions.
-- ============================================================================

CREATE TABLE pegging_runs (
  id                   uuid PRIMARY KEY,
  sales_order_id       uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  status               text NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
  as_of                timestamptz NOT NULL,
  horizon_days         integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
  result_hash          text CHECK (result_hash IS NULL OR length(result_hash)=64),
  error_text           text NOT NULL DEFAULT '',
  generated_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  generated_by         text NOT NULL,
  completed_at         timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pegging_runs_order_idx ON pegging_runs(sales_order_id, created_at DESC, id DESC);
CREATE INDEX pegging_runs_status_idx ON pegging_runs(status, created_at DESC);

CREATE TABLE pegging_nodes (
  id            uuid PRIMARY KEY,
  run_id        uuid NOT NULL REFERENCES pegging_runs(id) ON DELETE RESTRICT,
  node_key      text NOT NULL,
  node_type     text NOT NULL CHECK (node_type IN (
    'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
    'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
    'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE'
  )),
  entity_id     uuid,
  entity_ref    text NOT NULL DEFAULT '',
  item_id       uuid REFERENCES items(id) ON DELETE RESTRICT,
  item_code     text NOT NULL DEFAULT '',
  label         text NOT NULL,
  quantity      numeric(18,6),
  due_date      date,
  status        text NOT NULL DEFAULT '',
  detail        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,node_key)
);
CREATE INDEX pegging_nodes_run_type_idx ON pegging_nodes(run_id,node_type,node_key);
CREATE INDEX pegging_nodes_entity_idx ON pegging_nodes(node_type,entity_id) WHERE entity_id IS NOT NULL;
CREATE INDEX pegging_nodes_item_idx ON pegging_nodes(item_id,run_id) WHERE item_id IS NOT NULL;

CREATE TABLE pegging_edges (
  id            uuid PRIMARY KEY,
  run_id        uuid NOT NULL REFERENCES pegging_runs(id) ON DELETE RESTRICT,
  from_node_id  uuid NOT NULL REFERENCES pegging_nodes(id) ON DELETE RESTRICT,
  to_node_id    uuid NOT NULL REFERENCES pegging_nodes(id) ON DELETE RESTRICT,
  edge_type     text NOT NULL CHECK (edge_type IN (
    'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
    'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
    'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY'
  )),
  quantity      numeric(18,6),
  detail        jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK (from_node_id<>to_node_id),
  UNIQUE(run_id,from_node_id,to_node_id,edge_type)
);
CREATE INDEX pegging_edges_from_idx ON pegging_edges(run_id,from_node_id);
CREATE INDEX pegging_edges_to_idx ON pegging_edges(run_id,to_node_id);

CREATE TABLE planning_exceptions (
  id                    uuid PRIMARY KEY,
  run_id                uuid NOT NULL REFERENCES pegging_runs(id) ON DELETE RESTRICT,
  sales_order_id        uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  sales_order_line_id   uuid REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  exception_key         text NOT NULL,
  exception_type        text NOT NULL CHECK (exception_type IN (
    'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
    'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
    'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED'
  )),
  severity              text NOT NULL CHECK (severity IN ('INFO','WARNING','CRITICAL')),
  root_node_id          uuid NOT NULL REFERENCES pegging_nodes(id) ON DELETE RESTRICT,
  message               text NOT NULL,
  requested_date        date,
  promised_date         date,
  impact_date           date,
  impact_days           integer NOT NULL DEFAULT 0 CHECK (impact_days>=0),
  root_cause_path       jsonb NOT NULL DEFAULT '[]'::jsonb,
  detail                jsonb NOT NULL DEFAULT '{}'::jsonb,
  detected_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,exception_key)
);
CREATE INDEX planning_exceptions_run_idx ON planning_exceptions(run_id,severity,exception_type);
CREATE INDEX planning_exceptions_order_idx ON planning_exceptions(sales_order_id,detected_at DESC);
CREATE INDEX planning_exceptions_line_idx ON planning_exceptions(sales_order_line_id,detected_at DESC) WHERE sales_order_line_id IS NOT NULL;

CREATE TABLE planning_exception_actions (
  id                  uuid PRIMARY KEY,
  exception_id        uuid NOT NULL REFERENCES planning_exceptions(id) ON DELETE RESTRICT,
  action_type         text NOT NULL CHECK (action_type IN ('ACKNOWLEDGE','RESOLVE','REOPEN')),
  from_status         text NOT NULL CHECK (from_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
  to_status           text NOT NULL CHECK (to_status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
  actor_user_id       uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  actor_username      text NOT NULL,
  comment             text NOT NULL DEFAULT '',
  occurred_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX planning_exception_actions_exc_idx ON planning_exception_actions(exception_id,occurred_at,id);

CREATE OR REPLACE FUNCTION reject_pegging_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only planning evidence', TG_TABLE_NAME USING ERRCODE='23514';
END$$;

CREATE TRIGGER pegging_nodes_append_only_trg
BEFORE UPDATE OR DELETE ON pegging_nodes
FOR EACH ROW EXECUTE FUNCTION reject_pegging_evidence_mutation();
CREATE TRIGGER pegging_edges_append_only_trg
BEFORE UPDATE OR DELETE ON pegging_edges
FOR EACH ROW EXECUTE FUNCTION reject_pegging_evidence_mutation();
CREATE TRIGGER planning_exceptions_append_only_trg
BEFORE UPDATE OR DELETE ON planning_exceptions
FOR EACH ROW EXECUTE FUNCTION reject_pegging_evidence_mutation();
CREATE TRIGGER planning_exception_actions_append_only_trg
BEFORE UPDATE OR DELETE ON planning_exception_actions
FOR EACH ROW EXECUTE FUNCTION reject_pegging_evidence_mutation();

CREATE OR REPLACE FUNCTION guard_pegging_run_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'pegging_runs is append-only' USING ERRCODE='23514';
  END IF;
  IF OLD.status<>'RUNNING' THEN
    RAISE EXCEPTION 'completed pegging run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.sales_order_id IS DISTINCT FROM OLD.sales_order_id OR
     NEW.as_of IS DISTINCT FROM OLD.as_of OR NEW.horizon_days IS DISTINCT FROM OLD.horizon_days OR
     NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR NEW.generated_by IS DISTINCT FROM OLD.generated_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'pegging run request fields are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('SUCCEEDED','FAILED') THEN
    RAISE EXCEPTION 'pegging run may only transition RUNNING -> SUCCEEDED/FAILED' USING ERRCODE='23514';
  END IF;
  IF NEW.status='SUCCEEDED' AND (NEW.result_hash IS NULL OR length(NEW.result_hash)<>64 OR NEW.completed_at IS NULL) THEN
    RAISE EXCEPTION 'successful pegging run requires result_hash and completed_at' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER pegging_run_guard_trg
BEFORE UPDATE OR DELETE ON pegging_runs
FOR EACH ROW EXECUTE FUNCTION guard_pegging_run_mutation();

CREATE OR REPLACE FUNCTION guard_pegging_child_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM pegging_runs WHERE id=NEW.run_id FOR SHARE;
  IF s IS DISTINCT FROM 'RUNNING' THEN
    RAISE EXCEPTION 'pegging evidence may only be inserted while run is RUNNING' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER pegging_nodes_insert_guard_trg
BEFORE INSERT ON pegging_nodes FOR EACH ROW EXECUTE FUNCTION guard_pegging_child_insert();
CREATE TRIGGER pegging_edges_insert_guard_trg
BEFORE INSERT ON pegging_edges FOR EACH ROW EXECUTE FUNCTION guard_pegging_child_insert();
CREATE TRIGGER planning_exceptions_insert_guard_trg
BEFORE INSERT ON planning_exceptions FOR EACH ROW EXECUTE FUNCTION guard_pegging_child_insert();

CREATE OR REPLACE FUNCTION guard_planning_exception_consistency()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE run_order uuid; root_run uuid; line_order uuid;
BEGIN
  SELECT sales_order_id INTO run_order FROM pegging_runs WHERE id=NEW.run_id;
  SELECT run_id INTO root_run FROM pegging_nodes WHERE id=NEW.root_node_id;
  IF run_order IS DISTINCT FROM NEW.sales_order_id THEN
    RAISE EXCEPTION 'planning exception sales_order_id must match pegging run' USING ERRCODE='23514';
  END IF;
  IF root_run IS DISTINCT FROM NEW.run_id THEN
    RAISE EXCEPTION 'planning exception root node must belong to the same pegging run' USING ERRCODE='23514';
  END IF;
  IF NEW.sales_order_line_id IS NOT NULL THEN
    SELECT sales_order_id INTO line_order FROM sales_order_lines WHERE id=NEW.sales_order_line_id;
    IF line_order IS DISTINCT FROM NEW.sales_order_id THEN
      RAISE EXCEPTION 'planning exception line must belong to its Sales Order' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER planning_exception_consistency_trg
BEFORE INSERT ON planning_exceptions FOR EACH ROW EXECUTE FUNCTION guard_planning_exception_consistency();

CREATE OR REPLACE FUNCTION guard_pegging_edge_run()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fr uuid; tr uuid;
BEGIN
  SELECT run_id INTO fr FROM pegging_nodes WHERE id=NEW.from_node_id;
  SELECT run_id INTO tr FROM pegging_nodes WHERE id=NEW.to_node_id;
  IF fr IS DISTINCT FROM NEW.run_id OR tr IS DISTINCT FROM NEW.run_id THEN
    RAISE EXCEPTION 'pegging edge endpoints must belong to the same run' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER pegging_edges_run_guard_trg
BEFORE INSERT ON pegging_edges FOR EACH ROW EXECUTE FUNCTION guard_pegging_edge_run();

CREATE OR REPLACE FUNCTION planning_exception_current_status(p_exception uuid)
RETURNS text LANGUAGE sql STABLE AS $$
  SELECT COALESCE((
    SELECT a.to_status
      FROM planning_exception_actions a
     WHERE a.exception_id=p_exception
     ORDER BY a.occurred_at DESC,a.id DESC
     LIMIT 1
  ),'OPEN')
$$;

CREATE OR REPLACE FUNCTION guard_planning_exception_action()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur text; uname text; active boolean; urole text;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('cpim:planning-exception:'||NEW.exception_id::text,0));
  IF NOT EXISTS (SELECT 1 FROM planning_exceptions WHERE id=NEW.exception_id) THEN
    RAISE EXCEPTION 'planning exception % does not exist',NEW.exception_id USING ERRCODE='23503';
  END IF;
  cur := planning_exception_current_status(NEW.exception_id);
  NEW.from_status := cur;
  IF NEW.action_type='ACKNOWLEDGE' THEN
    IF cur<>'OPEN' OR NEW.to_status<>'ACKNOWLEDGED' THEN
      RAISE EXCEPTION 'ACKNOWLEDGE requires OPEN -> ACKNOWLEDGED' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.action_type='RESOLVE' THEN
    IF cur NOT IN ('OPEN','ACKNOWLEDGED') OR NEW.to_status<>'RESOLVED' THEN
      RAISE EXCEPTION 'RESOLVE requires OPEN/ACKNOWLEDGED -> RESOLVED' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.action_type='REOPEN' THEN
    IF cur<>'RESOLVED' OR NEW.to_status<>'OPEN' THEN
      RAISE EXCEPTION 'REOPEN requires RESOLVED -> OPEN' USING ERRCODE='23514';
    END IF;
  END IF;
  SELECT username,is_active,role INTO uname,active,urole FROM users WHERE id=NEW.actor_user_id;
  IF NOT FOUND OR NOT active OR uname IS DISTINCT FROM NEW.actor_username THEN
    RAISE EXCEPTION 'planning exception actor must be an active matching user' USING ERRCODE='23514';
  END IF;
  IF urole NOT IN ('admin','planner') THEN
    RAISE EXCEPTION 'planning exception actions require planner/admin actor' USING ERRCODE='23514';
  END IF;
  NEW.occurred_at := transaction_timestamp();
  RETURN NEW;
END$$;
CREATE TRIGGER planning_exception_action_guard_trg
BEFORE INSERT ON planning_exception_actions
FOR EACH ROW EXECUTE FUNCTION guard_planning_exception_action();

-- Current exception dashboard: only exceptions from the latest successful
-- pegging snapshot per Sales Order are operationally current. Historical runs
-- remain queryable by run id for audit/forensics.
CREATE OR REPLACE VIEW v_current_planning_exceptions AS
WITH latest_run AS (
  SELECT DISTINCT ON (sales_order_id) id,sales_order_id
    FROM pegging_runs
   WHERE status='SUCCEEDED'
   ORDER BY sales_order_id,completed_at DESC,id DESC
)
SELECT e.*,
       planning_exception_current_status(e.id) AS current_status,
       a.action_type AS latest_action_type,
       a.actor_username AS latest_actor,
       a.comment AS latest_comment,
       a.occurred_at AS latest_action_at,
       so.order_no AS sales_order_no,
       c.customer_no,
       c.name AS customer_name,
       l.line_no,
       i.code AS item_code,
       i.name AS item_name
  FROM latest_run lr
  JOIN planning_exceptions e ON e.run_id=lr.id
  JOIN sales_orders so ON so.id=e.sales_order_id
  JOIN customers c ON c.id=so.customer_id
  LEFT JOIN sales_order_lines l ON l.id=e.sales_order_line_id
  LEFT JOIN items i ON i.id=l.item_id
  LEFT JOIN LATERAL (
    SELECT x.action_type,x.actor_username,x.comment,x.occurred_at
      FROM planning_exception_actions x
     WHERE x.exception_id=e.id
     ORDER BY x.occurred_at DESC,x.id DESC
     LIMIT 1
  ) a ON true
 WHERE so.status IN ('CONFIRMED','PARTIALLY_SHIPPED');
