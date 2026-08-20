-- 0033 Backorder Processing + Product Allocation
-- Re-evaluate committed demand after supply/capacity changes, prioritize demand,
-- protect scarce ATP by customer service class, and publish only after stale-check.

CREATE TABLE customer_service_classes (
  code          text PRIMARY KEY CHECK (code ~ '^[A-Z][A-Z0-9_]{0,31}$'),
  name          text NOT NULL,
  priority_rank integer NOT NULL CHECK (priority_rank > 0),
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now()
);
INSERT INTO customer_service_classes(code,name,priority_rank) VALUES
  ('STRATEGIC','Strategic',1),
  ('STANDARD','Standard',2),
  ('OTHER','Other',3)
ON CONFLICT (code) DO NOTHING;

ALTER TABLE customers
  ADD COLUMN service_class_code text NOT NULL DEFAULT 'STANDARD'
  REFERENCES customer_service_classes(code) ON DELETE RESTRICT;
CREATE INDEX customers_service_class_idx ON customers(service_class_code,status);

ALTER TABLE sales_orders
  ADD COLUMN priority text NOT NULL DEFAULT 'NORMAL'
  CHECK (priority IN ('EXPEDITE','HIGH','NORMAL'));
CREATE INDEX sales_orders_bop_priority_idx ON sales_orders(status,priority,requested_date,order_date);

CREATE TABLE product_allocation_plans (
  id                   uuid PRIMARY KEY,
  item_id              uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  name                 text NOT NULL,
  effective_from       date NOT NULL,
  effective_to         date NOT NULL,
  status               text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','ACTIVE','INACTIVE')),
  created_by_user_id   uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_by           text NOT NULL,
  activated_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by         text,
  activated_at         timestamptz,
  deactivated_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  deactivated_by       text,
  deactivated_at       timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (effective_to >= effective_from),
  CHECK ((status <> 'ACTIVE') OR (activated_at IS NOT NULL AND activated_by_user_id IS NOT NULL AND activated_by IS NOT NULL)),
  CHECK ((status <> 'INACTIVE') OR (deactivated_at IS NOT NULL AND deactivated_by_user_id IS NOT NULL AND deactivated_by IS NOT NULL))
);
CREATE INDEX product_allocation_plans_item_dates_idx ON product_allocation_plans(item_id,status,effective_from,effective_to);

CREATE TABLE product_allocation_buckets (
  id                 uuid PRIMARY KEY,
  plan_id            uuid NOT NULL REFERENCES product_allocation_plans(id) ON DELETE CASCADE,
  service_class_code text NOT NULL REFERENCES customer_service_classes(code) ON DELETE RESTRICT,
  allocation_pct     numeric(9,6) NOT NULL CHECK (allocation_pct > 0 AND allocation_pct <= 100),
  priority_rank      integer NOT NULL CHECK (priority_rank > 0),
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE(plan_id,service_class_code),
  UNIQUE(plan_id,priority_rank)
);

CREATE OR REPLACE FUNCTION guard_product_allocation_plan()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pct numeric; overlapping uuid;
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.status <> 'DRAFT' THEN
      RAISE EXCEPTION 'only DRAFT product allocation plans may be deleted' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.status='DRAFT' THEN
    IF NEW.status='ACTIVE' THEN
      -- Serialize activations for the same item so two concurrent transactions
      -- cannot both pass the overlap check and create overlapping ACTIVE plans.
      PERFORM 1 FROM items WHERE id=OLD.item_id FOR UPDATE;
      SELECT COALESCE(SUM(allocation_pct),0) INTO pct FROM product_allocation_buckets WHERE plan_id=OLD.id;
      IF ABS(pct-100) > 0.000001 THEN
        RAISE EXCEPTION 'product allocation bucket percentages must total 100 before activation (got %)',pct USING ERRCODE='23514';
      END IF;
      SELECT p.id INTO overlapping
        FROM product_allocation_plans p
       WHERE p.id<>OLD.id AND p.item_id=OLD.item_id AND p.status='ACTIVE'
         AND daterange(p.effective_from,p.effective_to,'[]') && daterange(OLD.effective_from,OLD.effective_to,'[]')
       LIMIT 1;
      IF overlapping IS NOT NULL THEN
        RAISE EXCEPTION 'active product allocation plans may not overlap for the same item' USING ERRCODE='23514';
      END IF;
      IF NEW.item_id IS DISTINCT FROM OLD.item_id OR NEW.name IS DISTINCT FROM OLD.name OR
         NEW.effective_from IS DISTINCT FROM OLD.effective_from OR NEW.effective_to IS DISTINCT FROM OLD.effective_to OR
         NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
         NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'activation may not modify product allocation plan definition' USING ERRCODE='23514';
      END IF;
      RETURN NEW;
    END IF;
    IF NEW.status<>'DRAFT' THEN
      RAISE EXCEPTION 'invalid product allocation plan transition' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.status='ACTIVE' AND NEW.status='INACTIVE' THEN
    IF NEW.item_id IS DISTINCT FROM OLD.item_id OR NEW.name IS DISTINCT FROM OLD.name OR
       NEW.effective_from IS DISTINCT FROM OLD.effective_from OR NEW.effective_to IS DISTINCT FROM OLD.effective_to OR
       NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
       NEW.activated_by_user_id IS DISTINCT FROM OLD.activated_by_user_id OR NEW.activated_by IS DISTINCT FROM OLD.activated_by OR
       NEW.activated_at IS DISTINCT FROM OLD.activated_at OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
      RAISE EXCEPTION 'active product allocation plan definition is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'non-DRAFT product allocation plan is immutable' USING ERRCODE='23514';
END$$;
CREATE TRIGGER product_allocation_plan_guard_trg
BEFORE UPDATE OR DELETE ON product_allocation_plans
FOR EACH ROW EXECUTE FUNCTION guard_product_allocation_plan();

CREATE OR REPLACE FUNCTION guard_product_allocation_bucket()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  IF TG_OP='DELETE' THEN
    SELECT status INTO s FROM product_allocation_plans WHERE id=OLD.plan_id;
  ELSE
    SELECT status INTO s FROM product_allocation_plans WHERE id=NEW.plan_id;
  END IF;
  IF s IS DISTINCT FROM 'DRAFT' THEN
    RAISE EXCEPTION 'product allocation buckets are immutable after plan activation' USING ERRCODE='23514';
  END IF;
  RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END$$;
CREATE TRIGGER product_allocation_bucket_guard_trg
BEFORE INSERT OR UPDATE OR DELETE ON product_allocation_buckets
FOR EACH ROW EXECUTE FUNCTION guard_product_allocation_bucket();

CREATE TABLE backorder_runs (
  id                   uuid PRIMARY KEY,
  status               text NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
  horizon_days         integer NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
  filter_item_id       uuid REFERENCES items(id) ON DELETE RESTRICT,
  requested_at         timestamptz NOT NULL,
  completed_at         timestamptz,
  result_hash          text CHECK (result_hash IS NULL OR length(result_hash)=64),
  error_text           text NOT NULL DEFAULT '',
  requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  requested_by         text NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backorder_runs_requested_idx ON backorder_runs(requested_at DESC,id DESC);

CREATE TABLE backorder_run_lines (
  id                    uuid PRIMARY KEY,
  run_id                uuid NOT NULL REFERENCES backorder_runs(id) ON DELETE RESTRICT,
  sales_order_id        uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  sales_order_line_id   uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  item_id               uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  customer_id           uuid NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  service_class_code    text NOT NULL REFERENCES customer_service_classes(code) ON DELETE RESTRICT,
  order_priority        text NOT NULL CHECK (order_priority IN ('EXPEDITE','HIGH','NORMAL')),
  rank_no               integer NOT NULL CHECK (rank_no > 0),
  open_qty              numeric(18,6) NOT NULL CHECK (open_qty > 0),
  allocated_qty         numeric(18,6) NOT NULL DEFAULT 0 CHECK (allocated_qty >= 0),
  current_promised_date date,
  proposed_promised_date date,
  atp_qty               numeric(18,6) NOT NULL DEFAULT 0 CHECK (atp_qty >= 0),
  ctp_qty               numeric(18,6) NOT NULL DEFAULT 0 CHECK (ctp_qty >= 0),
  backorder_qty         numeric(18,6) NOT NULL DEFAULT 0 CHECK (backorder_qty >= 0),
  decision              text NOT NULL CHECK (decision IN ('UNCHANGED','IMPROVED','DELAYED','NEW_PROMISE','BACKORDER')),
  constraint_type       text NOT NULL DEFAULT 'NONE' CHECK (constraint_type IN ('NONE','PRODUCT_ALLOCATION','MATERIAL','CAPACITY','MATERIAL_AND_CAPACITY','HORIZON')),
  allocation_plan_id    uuid REFERENCES product_allocation_plans(id) ON DELETE RESTRICT,
  allocation_bucket_pct numeric(9,6),
  detail                jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,sales_order_line_id),
  CHECK (ABS((allocated_qty + atp_qty + ctp_qty + backorder_qty) - open_qty) <= 0.000001)
);
CREATE INDEX backorder_run_lines_order_idx ON backorder_run_lines(sales_order_id,run_id);
CREATE INDEX backorder_run_lines_item_idx ON backorder_run_lines(item_id,run_id,rank_no);

CREATE TABLE backorder_run_confirmations (
  id                  uuid PRIMARY KEY,
  run_id              uuid NOT NULL REFERENCES backorder_runs(id) ON DELETE RESTRICT,
  sales_order_line_id uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  sequence_no         integer NOT NULL CHECK (sequence_no > 0),
  quantity            numeric(18,6) NOT NULL CHECK (quantity > 0),
  confirmed_date      date NOT NULL,
  source              text NOT NULL CHECK (source IN ('ALLOCATED','ATP','CTP_PRODUCTION','CTP_PURCHASE','CTP_MIXED')),
  created_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,sales_order_line_id,sequence_no)
);
CREATE INDEX backorder_run_confirmations_line_idx ON backorder_run_confirmations(sales_order_line_id,run_id,sequence_no);

CREATE TABLE backorder_publications (
  id                   uuid PRIMARY KEY,
  run_id               uuid NOT NULL UNIQUE REFERENCES backorder_runs(id) ON DELETE RESTRICT,
  result_hash          text NOT NULL CHECK (length(result_hash)=64),
  published_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  published_by         text NOT NULL,
  published_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backorder_publications_published_idx ON backorder_publications(published_at DESC,id DESC);

CREATE OR REPLACE FUNCTION guard_backorder_run_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'backorder_runs is append-only' USING ERRCODE='23514';
  END IF;
  IF OLD.status <> 'RUNNING' THEN
    RAISE EXCEPTION 'completed backorder run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.horizon_days IS DISTINCT FROM OLD.horizon_days OR
     NEW.filter_item_id IS DISTINCT FROM OLD.filter_item_id OR NEW.requested_at IS DISTINCT FROM OLD.requested_at OR
     NEW.requested_by_user_id IS DISTINCT FROM OLD.requested_by_user_id OR NEW.requested_by IS DISTINCT FROM OLD.requested_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'backorder run request fields are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('SUCCEEDED','FAILED') THEN
    RAISE EXCEPTION 'backorder run may only transition RUNNING -> SUCCEEDED/FAILED' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER backorder_run_guard_trg
BEFORE UPDATE OR DELETE ON backorder_runs
FOR EACH ROW EXECUTE FUNCTION guard_backorder_run_mutation();

CREATE TRIGGER backorder_run_lines_append_only_trg
BEFORE UPDATE OR DELETE ON backorder_run_lines
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
CREATE TRIGGER backorder_run_confirmations_append_only_trg
BEFORE UPDATE OR DELETE ON backorder_run_confirmations
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
CREATE TRIGGER backorder_publications_append_only_trg
BEFORE UPDATE OR DELETE ON backorder_publications
FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();

-- 0032 already permits OrderPromising.Accept to revise promised dates. 0033 adds
-- a second, narrowly-scoped write context used only by Backorder.Publish.
CREATE OR REPLACE FUNCTION guard_sales_order_header_master_edit()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' AND (
     NEW.order_no IS DISTINCT FROM OLD.order_no OR
     NEW.customer_id IS DISTINCT FROM OLD.customer_id OR
     NEW.order_date IS DISTINCT FROM OLD.order_date OR
     NEW.requested_date IS DISTINCT FROM OLD.requested_date OR
     (NEW.promised_date IS DISTINCT FROM OLD.promised_date AND
       COALESCE(current_setting('cpim.promise_accept',true),'') <> 'on' AND
       COALESCE(current_setting('cpim.bop_publish',true),'') <> 'on')) THEN
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
      (NEW.promised_date IS DISTINCT FROM OLD.promised_date AND
        COALESCE(current_setting('cpim.promise_accept',true),'') <> 'on' AND
        COALESCE(current_setting('cpim.bop_publish',true),'') <> 'on') OR
      NEW.line_no IS DISTINCT FROM OLD.line_no OR NEW.sales_order_id IS DISTINCT FROM OLD.sales_order_id) THEN
    RAISE EXCEPTION 'confirmed sales order business fields are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

-- A newer BOP publication supersedes an older accepted promise for date
-- reconciliation. A newer Promise acceptance in turn supersedes older BOP evidence.
CREATE OR REPLACE FUNCTION assert_sales_order_promise_acceptance(p_order uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE a order_promise_acceptances%ROWTYPE; bad record; expected_header date;
BEGIN
  SELECT * INTO a FROM order_promise_acceptances
   WHERE sales_order_id=p_order ORDER BY accepted_at DESC,id DESC LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;

  IF EXISTS (
    SELECT 1 FROM backorder_publications p
    JOIN backorder_run_lines l ON l.run_id=p.run_id
    WHERE l.sales_order_id=p_order AND p.published_at>a.accepted_at
  ) THEN
    RETURN;
  END IF;

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

CREATE OR REPLACE FUNCTION assert_backorder_publication(p_order uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE p backorder_publications%ROWTYPE; bad record; expected_header date;
BEGIN
  SELECT bp.* INTO p
    FROM backorder_publications bp
   WHERE EXISTS (SELECT 1 FROM backorder_run_lines l WHERE l.run_id=bp.run_id AND l.sales_order_id=p_order)
   ORDER BY bp.published_at DESC,bp.id DESC LIMIT 1;
  IF NOT FOUND THEN RETURN; END IF;

  IF EXISTS (
    SELECT 1 FROM order_promise_acceptances a
    WHERE a.sales_order_id=p_order AND a.accepted_at>p.published_at
  ) THEN
    RETURN;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM backorder_runs r WHERE r.id=p.run_id AND r.status='SUCCEEDED' AND r.result_hash=p.result_hash
  ) THEN
    RAISE EXCEPTION 'BOP publication must match a successful run' USING ERRCODE='23514';
  END IF;

  SELECT l.sales_order_line_id AS id,l.proposed_promised_date AS expected,sl.promised_date
    INTO bad
    FROM backorder_run_lines l
    JOIN sales_order_lines sl ON sl.id=l.sales_order_line_id
    LEFT JOIN backorder_run_confirmations c ON c.run_id=l.run_id AND c.sales_order_line_id=l.sales_order_line_id
   WHERE l.run_id=p.run_id AND l.sales_order_id=p_order
   GROUP BY l.sales_order_line_id,l.open_qty,l.allocated_qty,l.atp_qty,l.ctp_qty,l.backorder_qty,l.proposed_promised_date,sl.promised_date
  HAVING ABS(COALESCE(SUM(c.quantity),0) - (l.allocated_qty+l.atp_qty+l.ctp_qty)) > 0.000001
      OR ABS((l.allocated_qty+l.atp_qty+l.ctp_qty+l.backorder_qty)-l.open_qty) > 0.000001
      OR (l.backorder_qty <= 0.000001 AND l.proposed_promised_date IS DISTINCT FROM MAX(c.confirmed_date))
      OR (l.backorder_qty > 0.000001 AND l.proposed_promised_date IS NOT NULL)
      OR sl.promised_date IS DISTINCT FROM l.proposed_promised_date
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'published BOP proposal does not reconcile sales order line % (actual %, expected %)',bad.id,bad.promised_date,bad.expected USING ERRCODE='23514';
  END IF;

  SELECT MAX(promised_date) INTO expected_header FROM sales_order_lines WHERE sales_order_id=p_order;
  IF EXISTS (SELECT 1 FROM sales_orders WHERE id=p_order AND promised_date IS DISTINCT FROM expected_header) THEN
    RAISE EXCEPTION 'sales order header promised_date does not match published BOP result' USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_backorder_publication()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p_order uuid;
BEGIN
  IF TG_TABLE_NAME='backorder_publications' THEN
    FOR p_order IN SELECT DISTINCT sales_order_id FROM backorder_run_lines WHERE run_id=NEW.run_id LOOP
      PERFORM assert_backorder_publication(p_order);
    END LOOP;
    RETURN NULL;
  END IF;
  IF TG_TABLE_NAME='sales_orders' THEN p_order:=COALESCE(NEW.id,OLD.id);
  ELSE p_order:=COALESCE(NEW.sales_order_id,OLD.sales_order_id); END IF;
  PERFORM assert_backorder_publication(p_order);
  RETURN NULL;
END$$;

CREATE CONSTRAINT TRIGGER backorder_publication_reconcile_trg
AFTER INSERT ON backorder_publications
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_backorder_publication();
CREATE CONSTRAINT TRIGGER backorder_line_date_reconcile_trg
AFTER UPDATE ON sales_order_lines
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_backorder_publication();
CREATE CONSTRAINT TRIGGER backorder_header_date_reconcile_trg
AFTER UPDATE ON sales_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_backorder_publication();
