-- ============================================================================
-- 0035: Supplier Scheduling + Lead-Time Reliability
--
-- Adds immutable supplier schedule events (confirmation / revision / ASN /
-- cancellation), auditable lead-time reliability snapshots, and a canonical
-- planning schedule view consumed by MRP, CTP and Full Pegging.
-- ============================================================================

CREATE TABLE supplier_schedule_events (
  id                       uuid PRIMARY KEY,
  purchase_order_id        uuid NOT NULL REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  revision_no              integer NOT NULL CHECK (revision_no >= 1),
  event_type               text NOT NULL CHECK (event_type IN ('CONFIRM','REVISE','ASN','CANCEL')),
  quantity                 numeric(18,6),
  confirmed_delivery_date  date,
  asn_no                   text NOT NULL DEFAULT '',
  expected_arrival_date    date,
  supplier_reference       text NOT NULL DEFAULT '',
  notes                    text NOT NULL DEFAULT '',
  actor_user_id            uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  actor_username           text NOT NULL,
  occurred_at              timestamptz NOT NULL DEFAULT now(),
  created_at               timestamptz NOT NULL DEFAULT now(),
  UNIQUE(purchase_order_id, revision_no)
);
CREATE INDEX supplier_schedule_events_po_idx
  ON supplier_schedule_events(purchase_order_id, occurred_at DESC, revision_no DESC);
CREATE INDEX supplier_schedule_events_asn_idx
  ON supplier_schedule_events(purchase_order_id, asn_no, revision_no DESC)
  WHERE event_type='ASN' AND btrim(asn_no)<>'';

CREATE OR REPLACE FUNCTION guard_supplier_schedule_event()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  po_qty numeric;
  po_received numeric;
  po_status text;
  po_remaining numeric;
  urole text;
  uname text;
BEGIN
  IF TG_OP IN ('UPDATE','DELETE') THEN
    RAISE EXCEPTION 'supplier_schedule_events are immutable; append a new revision'
      USING ERRCODE='23514';
  END IF;

  SELECT quantity,received_qty,status INTO po_qty,po_received,po_status
    FROM purchase_orders WHERE id=NEW.purchase_order_id FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'purchase order % does not exist', NEW.purchase_order_id
      USING ERRCODE='23503';
  END IF;
  IF po_status IN ('RECEIVED','CLOSED') THEN
    RAISE EXCEPTION 'supplier schedule cannot be changed after PO is %', po_status
      USING ERRCODE='23514';
  END IF;

  SELECT role,username INTO urole,uname FROM users WHERE id=NEW.actor_user_id;
  IF urole IS NULL OR urole NOT IN ('admin','planner') OR uname IS DISTINCT FROM NEW.actor_username THEN
    RAISE EXCEPTION 'supplier schedule events require matching planner/admin actor'
      USING ERRCODE='42501';
  END IF;

  po_remaining := GREATEST(po_qty-COALESCE(po_received,0),0);

  IF NEW.event_type IN ('CONFIRM','REVISE') THEN
    IF NEW.quantity IS NULL OR NEW.quantity <= 0 OR ABS(NEW.quantity-po_remaining)>0.000001 THEN
      RAISE EXCEPTION 'supplier confirmation quantity must equal current PO remaining quantity %', po_remaining
        USING ERRCODE='23514';
    END IF;
    IF NEW.confirmed_delivery_date IS NULL THEN
      RAISE EXCEPTION 'supplier confirmation requires confirmed_delivery_date'
        USING ERRCODE='23514';
    END IF;
  ELSIF NEW.event_type='ASN' THEN
    IF NEW.quantity IS NULL OR NEW.quantity <= 0 OR ABS(NEW.quantity-po_remaining)>0.000001 THEN
      RAISE EXCEPTION 'ASN quantity must equal current PO remaining quantity %', po_remaining
        USING ERRCODE='23514';
    END IF;
    IF btrim(NEW.asn_no)='' OR NEW.expected_arrival_date IS NULL THEN
      RAISE EXCEPTION 'ASN requires asn_no and expected_arrival_date'
        USING ERRCODE='23514';
    END IF;
  ELSIF NEW.event_type='CANCEL' THEN
    IF NEW.quantity IS NOT NULL OR NEW.confirmed_delivery_date IS NOT NULL OR
       btrim(NEW.asn_no)<>'' OR NEW.expected_arrival_date IS NOT NULL THEN
      RAISE EXCEPTION 'CANCEL event must not carry quantity or delivery dates'
        USING ERRCODE='23514';
    END IF;
  END IF;

  RETURN NEW;
END$$;
CREATE TRIGGER supplier_schedule_events_guard_trg
BEFORE INSERT OR UPDATE OR DELETE ON supplier_schedule_events
FOR EACH ROW EXECUTE FUNCTION guard_supplier_schedule_event();

-- Latest effective supplier commitment and ASN for each PO. A CANCEL supersedes
-- confirmations/ASNs that occurred before it; a later new confirmation/ASN is valid.
CREATE OR REPLACE VIEW v_purchase_order_supplier_schedule AS
SELECT po.id AS purchase_order_id,
       CASE
         WHEN asn.occurred_at IS NOT NULL THEN 'ASN'
         WHEN conf.occurred_at IS NOT NULL THEN 'CONFIRMED'
         ELSE 'UNCONFIRMED'
       END AS schedule_status,
       conf.id AS confirmation_event_id,
       conf.quantity AS confirmed_quantity,
       conf.confirmed_delivery_date,
       conf.supplier_reference,
       conf.occurred_at AS confirmed_at,
       asn.id AS asn_event_id,
       asn.asn_no,
       asn.quantity AS asn_quantity,
       asn.expected_arrival_date,
       asn.occurred_at AS asn_at,
       cancel_ev.occurred_at AS cancelled_at
  FROM purchase_orders po
  LEFT JOIN LATERAL (
    SELECT e.occurred_at,e.revision_no
      FROM supplier_schedule_events e
     WHERE e.purchase_order_id=po.id AND e.event_type='CANCEL'
     ORDER BY e.revision_no DESC LIMIT 1
  ) cancel_ev ON true
  LEFT JOIN LATERAL (
    SELECT e.*
      FROM supplier_schedule_events e
     WHERE e.purchase_order_id=po.id
       AND e.event_type IN ('CONFIRM','REVISE')
       AND (cancel_ev.revision_no IS NULL OR e.revision_no>cancel_ev.revision_no)
     ORDER BY e.revision_no DESC LIMIT 1
  ) conf ON true
  LEFT JOIN LATERAL (
    SELECT e.*
      FROM supplier_schedule_events e
     WHERE e.purchase_order_id=po.id
       AND e.event_type='ASN'
       AND (cancel_ev.revision_no IS NULL OR e.revision_no>cancel_ev.revision_no)
     ORDER BY e.revision_no DESC LIMIT 1
  ) asn ON true;

CREATE TABLE supplier_lead_time_runs (
  id                   uuid PRIMARY KEY,
  window_start         date NOT NULL,
  window_end           date NOT NULL,
  min_samples          integer NOT NULL CHECK (min_samples BETWEEN 1 AND 1000),
  status               text NOT NULL CHECK (status IN ('RUNNING','COMPLETE','FAILED')),
  result_hash          text CHECK (result_hash IS NULL OR length(result_hash)=64),
  generated_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  generated_by         text NOT NULL,
  completed_at         timestamptz,
  error_text           text NOT NULL DEFAULT '',
  created_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (window_end >= window_start)
);
CREATE INDEX supplier_lead_time_runs_idx ON supplier_lead_time_runs(created_at DESC,id DESC);

CREATE OR REPLACE FUNCTION guard_supplier_lead_time_run_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  urole text;
  uname text;
BEGIN
  SELECT role,username INTO urole,uname FROM users WHERE id=NEW.generated_by_user_id;
  IF urole IS NULL OR urole NOT IN ('admin','planner') OR uname IS DISTINCT FROM NEW.generated_by THEN
    RAISE EXCEPTION 'supplier lead-time run requires matching planner/admin actor'
      USING ERRCODE='42501';
  END IF;
  IF NEW.status<>'RUNNING' OR NEW.result_hash IS NOT NULL OR NEW.completed_at IS NOT NULL THEN
    RAISE EXCEPTION 'supplier lead-time run must be inserted in RUNNING state'
      USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER supplier_lead_time_runs_insert_guard_trg
BEFORE INSERT ON supplier_lead_time_runs
FOR EACH ROW EXECUTE FUNCTION guard_supplier_lead_time_run_insert();

CREATE TABLE supplier_lead_time_results (
  id                       uuid PRIMARY KEY,
  run_id                   uuid NOT NULL REFERENCES supplier_lead_time_runs(id) ON DELETE RESTRICT,
  supplier_name            text NOT NULL,
  item_id                  uuid REFERENCES items(id) ON DELETE RESTRICT,
  sample_count             integer NOT NULL CHECK (sample_count >= 0),
  average_lead_days        numeric(12,4) NOT NULL CHECK (average_lead_days >= 0),
  stddev_lead_days         numeric(12,4) NOT NULL CHECK (stddev_lead_days >= 0),
  p50_lead_days            numeric(12,4) NOT NULL CHECK (p50_lead_days >= 0),
  p90_lead_days            numeric(12,4) NOT NULL CHECK (p90_lead_days >= 0),
  on_time_rate             numeric(7,6) NOT NULL CHECK (on_time_rate BETWEEN 0 AND 1),
  average_lateness_days    numeric(12,4) NOT NULL CHECK (average_lateness_days >= 0),
  recommended_lead_days    integer NOT NULL CHECK (recommended_lead_days >= 0),
  confidence               text NOT NULL CHECK (confidence IN ('LOW','MEDIUM','HIGH')),
  created_at               timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX supplier_lead_time_result_key
  ON supplier_lead_time_results(run_id,supplier_name,COALESCE(item_id,'00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX supplier_lead_time_result_supplier_idx
  ON supplier_lead_time_results(supplier_name,item_id,run_id);

CREATE OR REPLACE FUNCTION reject_supplier_reliability_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only reliability evidence', TG_TABLE_NAME USING ERRCODE='23514';
END$$;
CREATE TRIGGER supplier_lead_time_results_append_only_trg
BEFORE UPDATE OR DELETE ON supplier_lead_time_results
FOR EACH ROW EXECUTE FUNCTION reject_supplier_reliability_evidence_mutation();

CREATE OR REPLACE FUNCTION guard_supplier_lead_time_run_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'supplier_lead_time_runs is append-only' USING ERRCODE='23514';
  END IF;
  IF OLD.status<>'RUNNING' THEN
    RAISE EXCEPTION 'completed supplier lead-time run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.window_start IS DISTINCT FROM OLD.window_start OR
     NEW.window_end IS DISTINCT FROM OLD.window_end OR NEW.min_samples IS DISTINCT FROM OLD.min_samples OR
     NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR
     NEW.generated_by IS DISTINCT FROM OLD.generated_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'supplier lead-time run request fields are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('COMPLETE','FAILED') THEN
    RAISE EXCEPTION 'supplier lead-time run may only transition RUNNING -> COMPLETE/FAILED' USING ERRCODE='23514';
  END IF;
  IF NEW.status='COMPLETE' AND (NEW.result_hash IS NULL OR length(NEW.result_hash)<>64 OR NEW.completed_at IS NULL) THEN
    RAISE EXCEPTION 'completed supplier lead-time run requires result_hash and completed_at' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER supplier_lead_time_runs_guard_trg
BEFORE UPDATE OR DELETE ON supplier_lead_time_runs
FOR EACH ROW EXECUTE FUNCTION guard_supplier_lead_time_run_mutation();

CREATE OR REPLACE FUNCTION guard_supplier_lead_time_result_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM supplier_lead_time_runs WHERE id=NEW.run_id FOR SHARE;
  IF s IS DISTINCT FROM 'RUNNING' THEN
    RAISE EXCEPTION 'supplier lead-time results may only be inserted while run is RUNNING' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER supplier_lead_time_results_insert_guard_trg
BEFORE INSERT ON supplier_lead_time_results
FOR EACH ROW EXECUTE FUNCTION guard_supplier_lead_time_result_insert();

-- Latest successful reliability snapshot. Prefer exact supplier+item; callers may
-- fall back to supplier-level (item_id IS NULL) when the exact sample is sparse.
CREATE OR REPLACE VIEW v_current_supplier_lead_time AS
WITH latest AS (
  SELECT id,min_samples,created_at
    FROM supplier_lead_time_runs
   WHERE status='COMPLETE'
   ORDER BY created_at DESC,id DESC
   LIMIT 1
)
SELECT r.*,l.min_samples,l.created_at AS run_created_at
  FROM supplier_lead_time_results r
  JOIN latest l ON l.id=r.run_id;

-- Canonical PO planning date consumed by MRP / CTP / Pegging.
-- ASN has strongest evidence, then supplier confirmation, then a conservative
-- reliability-adjusted date, with the original PO due date as the floor.
CREATE OR REPLACE VIEW v_purchase_order_planning_schedule AS
SELECT po.id,po.po_no,po.item_id,po.supplier,po.quantity,po.received_qty,
       GREATEST(po.quantity-po.received_qty,0) AS remaining_qty,
       po.order_date,po.due_date,po.status,po.received_lot_id,po.received_at,
       COALESCE(sq.status,'APPROVED') AS supplier_quality_status,
       COALESCE(ss.schedule_status,'UNCONFIRMED') AS schedule_status,
       ss.confirmation_event_id,ss.confirmed_quantity,ss.confirmed_delivery_date,
       ss.asn_event_id,COALESCE(ss.asn_no,'') AS asn_no,ss.asn_quantity,ss.expected_arrival_date AS asn_expected_arrival_date,
       COALESCE(exact.sample_count,fallback.sample_count,0) AS reliability_sample_count,
       COALESCE(exact.on_time_rate,fallback.on_time_rate,0)::double precision AS reliability_on_time_rate,
       COALESCE(exact.p90_lead_days,fallback.p90_lead_days,0)::double precision AS reliability_p90_days,
       COALESCE(exact.recommended_lead_days,fallback.recommended_lead_days,0) AS recommended_lead_time_days,
       CASE
         WHEN ss.expected_arrival_date IS NOT NULL THEN ss.expected_arrival_date
         WHEN ss.confirmed_delivery_date IS NOT NULL THEN ss.confirmed_delivery_date
         WHEN COALESCE(exact.sample_count,fallback.sample_count,0) >= COALESCE(exact.min_samples,fallback.min_samples,2147483647)
           THEN GREATEST(po.due_date, po.order_date + COALESCE(exact.recommended_lead_days,fallback.recommended_lead_days,0))
         ELSE po.due_date
       END AS expected_delivery_date,
       CASE
         WHEN ss.expected_arrival_date IS NOT NULL THEN 'ASN'
         WHEN ss.confirmed_delivery_date IS NOT NULL THEN 'SUPPLIER_CONFIRMATION'
         WHEN COALESCE(exact.sample_count,fallback.sample_count,0) >= COALESCE(exact.min_samples,fallback.min_samples,2147483647) THEN 'RELIABILITY'
         ELSE 'PO_DUE_DATE'
       END AS schedule_source
  FROM purchase_orders po
  LEFT JOIN supplier_quality_profiles sq ON sq.supplier_name=btrim(po.supplier)
  LEFT JOIN v_purchase_order_supplier_schedule ss ON ss.purchase_order_id=po.id
  LEFT JOIN v_current_supplier_lead_time exact
    ON exact.supplier_name=btrim(po.supplier) AND exact.item_id=po.item_id
  LEFT JOIN v_current_supplier_lead_time fallback
    ON fallback.supplier_name=btrim(po.supplier) AND fallback.item_id IS NULL;

-- Extend 0034 graph vocabulary for supplier schedule and reliability evidence.
ALTER TABLE pegging_nodes DROP CONSTRAINT IF EXISTS pegging_nodes_node_type_check;
ALTER TABLE pegging_nodes ADD CONSTRAINT pegging_nodes_node_type_check CHECK (node_type IN (
  'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
  'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
  'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE',
  'SUPPLIER_CONFIRMATION','SUPPLIER_ASN','LEAD_TIME_PROFILE'
));

ALTER TABLE pegging_edges DROP CONSTRAINT IF EXISTS pegging_edges_edge_type_check;
ALTER TABLE pegging_edges ADD CONSTRAINT pegging_edges_edge_type_check CHECK (edge_type IN (
  'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
  'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
  'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY',
  'CONFIRMED_BY','SHIPPED_BY','PLANNED_USING'
));

ALTER TABLE planning_exceptions DROP CONSTRAINT IF EXISTS planning_exceptions_exception_type_check;
ALTER TABLE planning_exceptions ADD CONSTRAINT planning_exceptions_exception_type_check CHECK (exception_type IN (
  'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
  'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
  'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED',
  'SUPPLIER_CONFIRMATION_LATE','SUPPLIER_RELIABILITY_RISK'
));
