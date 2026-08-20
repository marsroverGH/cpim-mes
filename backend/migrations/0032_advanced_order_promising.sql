-- 0032 Advanced Order Promising / Capable-to-Promise
--
-- Adds immutable ATP/CTP what-if evidence and controlled promise acceptance.
-- Promise checks may write only promise evidence; they must not create/modify
-- inventory, WO, PO, MRP or detailed-schedule operational records.

CREATE TABLE order_promise_runs (
  id                   uuid PRIMARY KEY,
  sales_order_id       uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  strategy             text NOT NULL DEFAULT 'ATP_THEN_CTP' CHECK (strategy IN ('ATP_THEN_CTP')),
  status               text NOT NULL DEFAULT 'RUNNING' CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
  requested_at         timestamptz NOT NULL DEFAULT now(),
  completed_at         timestamptz,
  horizon_days         integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
  result_hash          text,
  error_text           text NOT NULL DEFAULT '',
  requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_by         text NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CHECK ((status='RUNNING' AND completed_at IS NULL) OR (status IN ('SUCCEEDED','FAILED') AND completed_at IS NOT NULL)),
  CHECK ((status='SUCCEEDED' AND result_hash IS NOT NULL AND length(result_hash)=64) OR status<>'SUCCEEDED')
);
CREATE INDEX order_promise_runs_order_idx ON order_promise_runs(sales_order_id,requested_at DESC,id DESC);

CREATE TABLE order_promise_line_results (
  id                   uuid PRIMARY KEY,
  run_id               uuid NOT NULL REFERENCES order_promise_runs(id) ON DELETE RESTRICT,
  sales_order_line_id  uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  requested_qty        numeric(18,6) NOT NULL CHECK (requested_qty >= 0),
  requested_date       date NOT NULL,
  atp_qty              numeric(18,6) NOT NULL DEFAULT 0 CHECK (atp_qty >= 0),
  ctp_qty              numeric(18,6) NOT NULL DEFAULT 0 CHECK (ctp_qty >= 0),
  earliest_full_date   date,
  promise_method       text NOT NULL CHECK (promise_method IN ('ATP','ATP_CTP','CTP','UNAVAILABLE')),
  material_ready_date  date,
  capacity_ready_date  date,
  constraint_type      text NOT NULL DEFAULT 'NONE' CHECK (constraint_type IN ('NONE','MATERIAL','CAPACITY','MATERIAL_AND_CAPACITY','HORIZON')),
  constraint_detail    jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,sales_order_line_id),
  CHECK (atp_qty + ctp_qty <= requested_qty + 0.000001)
);

CREATE TABLE order_promise_confirmations (
  id                   uuid PRIMARY KEY,
  run_id               uuid NOT NULL REFERENCES order_promise_runs(id) ON DELETE RESTRICT,
  sales_order_line_id  uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  sequence_no          integer NOT NULL CHECK (sequence_no > 0),
  quantity             numeric(18,6) NOT NULL CHECK (quantity > 0),
  confirmed_date       date NOT NULL,
  source               text NOT NULL CHECK (source IN ('ON_HAND','ATP','CTP_PRODUCTION','CTP_PURCHASE','CTP_MIXED')),
  created_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,sales_order_line_id,sequence_no)
);
CREATE INDEX order_promise_confirmations_line_idx ON order_promise_confirmations(sales_order_line_id,confirmed_date);

CREATE TABLE order_promise_acceptances (
  id                  uuid PRIMARY KEY,
  run_id              uuid NOT NULL UNIQUE REFERENCES order_promise_runs(id) ON DELETE RESTRICT,
  sales_order_id      uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  result_hash         text NOT NULL CHECK (length(result_hash)=64),
  accepted_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  accepted_by         text NOT NULL,
  accepted_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_promise_acceptances_order_idx ON order_promise_acceptances(sales_order_id,accepted_at DESC,id DESC);

-- Promise result evidence is append-only. A run may only complete once.
CREATE OR REPLACE FUNCTION guard_order_promise_run_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'order_promise_runs is append-only' USING ERRCODE='23514';
  END IF;
  IF OLD.status <> 'RUNNING' THEN
    RAISE EXCEPTION 'completed order promise run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.sales_order_id IS DISTINCT FROM OLD.sales_order_id OR
     NEW.strategy IS DISTINCT FROM OLD.strategy OR NEW.requested_at IS DISTINCT FROM OLD.requested_at OR
     NEW.horizon_days IS DISTINCT FROM OLD.horizon_days OR
     NEW.requested_by_user_id IS DISTINCT FROM OLD.requested_by_user_id OR
     NEW.requested_by IS DISTINCT FROM OLD.requested_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'order promise run request fields are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('SUCCEEDED','FAILED') THEN
    RAISE EXCEPTION 'order promise run may only transition RUNNING -> SUCCEEDED/FAILED' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER order_promise_run_guard_trg
BEFORE UPDATE OR DELETE ON order_promise_runs
FOR EACH ROW EXECUTE FUNCTION guard_order_promise_run_mutation();

CREATE TRIGGER order_promise_line_results_append_only_trg
BEFORE UPDATE OR DELETE ON order_promise_line_results
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
CREATE TRIGGER order_promise_confirmations_append_only_trg
BEFORE UPDATE OR DELETE ON order_promise_confirmations
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
CREATE TRIGGER order_promise_acceptances_append_only_trg
BEFORE UPDATE OR DELETE ON order_promise_acceptances
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();

-- 0031 made confirmed Sales Order business dates immutable. 0032 narrows the
-- exception to a transaction explicitly marked by OrderPromising.Accept.
CREATE OR REPLACE FUNCTION guard_sales_order_header_master_edit()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' AND (
     NEW.order_no IS DISTINCT FROM OLD.order_no OR
     NEW.customer_id IS DISTINCT FROM OLD.customer_id OR
     NEW.order_date IS DISTINCT FROM OLD.order_date OR
     NEW.requested_date IS DISTINCT FROM OLD.requested_date OR
     (NEW.promised_date IS DISTINCT FROM OLD.promised_date AND COALESCE(current_setting('cpim.promise_accept',true),'') <> 'on')) THEN
    RAISE EXCEPTION 'confirmed sales order business fields are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION guard_sales_order_line_master_edit()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM sales_orders WHERE id=COALESCE(NEW.sales_order_id,OLD.sales_order_id);
  IF TG_OP <> 'DELETE' AND NOT EXISTS (SELECT 1 FROM items i WHERE i.id=NEW.item_id AND i.type IN ('FG','SA')) THEN
    RAISE EXCEPTION 'sales order item must be FG or SA' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN
    IF s <> 'DRAFT' THEN RAISE EXCEPTION 'confirmed sales order lines are immutable' USING ERRCODE='23514'; END IF;
    RETURN OLD;
  END IF;
  IF TG_OP='INSERT' THEN
    IF s <> 'DRAFT' THEN RAISE EXCEPTION 'lines can only be added to DRAFT sales orders' USING ERRCODE='23514'; END IF;
    RETURN NEW;
  END IF;
  IF s <> 'DRAFT' AND (
      NEW.item_id IS DISTINCT FROM OLD.item_id OR NEW.quantity IS DISTINCT FROM OLD.quantity OR
      NEW.unit_price IS DISTINCT FROM OLD.unit_price OR NEW.requested_date IS DISTINCT FROM OLD.requested_date OR
      (NEW.promised_date IS DISTINCT FROM OLD.promised_date AND COALESCE(current_setting('cpim.promise_accept',true),'') <> 'on') OR
      NEW.line_no IS DISTINCT FROM OLD.line_no OR NEW.sales_order_id IS DISTINCT FROM OLD.sales_order_id) THEN
    RAISE EXCEPTION 'confirmed sales order business fields are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

-- At commit, accepted evidence must exactly explain line/header promised dates.
CREATE OR REPLACE FUNCTION assert_sales_order_promise_acceptance(p_order uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE a order_promise_acceptances%ROWTYPE; bad record; expected_header date;
BEGIN
  SELECT * INTO a FROM order_promise_acceptances
   WHERE sales_order_id=p_order ORDER BY accepted_at DESC,id DESC LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;

  IF NOT EXISTS (
    SELECT 1 FROM order_promise_runs r
     WHERE r.id=a.run_id AND r.sales_order_id=p_order AND r.status='SUCCEEDED'
       AND r.result_hash=a.result_hash
  ) THEN
    RAISE EXCEPTION 'accepted promise must match a successful run for the same sales order' USING ERRCODE='23514';
  END IF;

  SELECT r.sales_order_line_id AS id,NULL::date AS promised_date,NULL::date AS expected
    INTO bad
    FROM order_promise_line_results r
    JOIN sales_order_lines l ON l.id=r.sales_order_line_id
    LEFT JOIN order_promise_confirmations c
      ON c.run_id=r.run_id AND c.sales_order_line_id=r.sales_order_line_id
   WHERE r.run_id=a.run_id
   GROUP BY r.sales_order_line_id,l.sales_order_id,r.requested_qty,r.atp_qty,r.ctp_qty,r.earliest_full_date
  HAVING l.sales_order_id <> p_order
      OR ABS(COALESCE(SUM(c.quantity),0) - (r.atp_qty+r.ctp_qty)) > 0.000001
      OR r.atp_qty+r.ctp_qty+0.000001 < r.requested_qty
      OR r.earliest_full_date IS DISTINCT FROM MAX(c.confirmed_date)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'accepted promise run has inconsistent or incomplete line evidence for %',bad.id USING ERRCODE='23514';
  END IF;

  SELECT l.id,l.promised_date,MAX(c.confirmed_date) AS expected
    INTO bad
    FROM order_promise_line_results r
    JOIN sales_order_lines l ON l.id=r.sales_order_line_id
    LEFT JOIN order_promise_confirmations c ON c.sales_order_line_id=l.id AND c.run_id=r.run_id
   WHERE r.run_id=a.run_id
   GROUP BY l.id,l.promised_date
  HAVING l.promised_date IS DISTINCT FROM MAX(c.confirmed_date)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'sales order line % promised_date % does not match accepted promise %',bad.id,bad.promised_date,bad.expected USING ERRCODE='23514';
  END IF;

  SELECT MAX(promised_date) INTO expected_header FROM sales_order_lines WHERE sales_order_id=p_order;
  IF EXISTS (SELECT 1 FROM sales_orders WHERE id=p_order AND promised_date IS DISTINCT FROM expected_header) THEN
    RAISE EXCEPTION 'sales order header promised_date does not match accepted promise' USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_sales_order_promise_acceptance()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p_order uuid;
BEGIN
  IF TG_TABLE_NAME='sales_orders' THEN
    p_order := COALESCE(NEW.id,OLD.id);
  ELSE
    p_order := COALESCE(NEW.sales_order_id,OLD.sales_order_id);
  END IF;
  PERFORM assert_sales_order_promise_acceptance(p_order);
  RETURN NULL;
END$$;

CREATE CONSTRAINT TRIGGER order_promise_acceptance_reconcile_trg
AFTER INSERT ON order_promise_acceptances
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_sales_order_promise_acceptance();

CREATE CONSTRAINT TRIGGER order_promise_line_date_reconcile_trg
AFTER UPDATE ON sales_order_lines
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_sales_order_promise_acceptance();

CREATE CONSTRAINT TRIGGER order_promise_header_date_reconcile_trg
AFTER UPDATE ON sales_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_sales_order_promise_acceptance();
