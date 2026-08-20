-- ============================================================================
-- 0031: Sales Order / Customer Order Management
--
-- Replaces unversioned demand_forecasts(source='ORDER') as the operational
-- source of committed customer demand. Sales orders own confirmation,
-- allocation, shipment and cancellation state; shipments use the unified
-- inventory/lot ledger.
-- ============================================================================

CREATE TABLE IF NOT EXISTS customers (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_no        text NOT NULL UNIQUE,
  name               text NOT NULL,
  status             text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','BLOCKED')),
  ship_to            text NOT NULL DEFAULT '',
  notes              text NOT NULL DEFAULT '',
  created_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  created_by         text NOT NULL DEFAULT '',
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sales_orders (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  order_no             text NOT NULL UNIQUE,
  customer_id          uuid NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  order_date           date NOT NULL DEFAULT current_date,
  requested_date       date NOT NULL,
  promised_date        date,
  status               text NOT NULL DEFAULT 'DRAFT'
                       CHECK (status IN ('DRAFT','CONFIRMED','PARTIALLY_SHIPPED','SHIPPED','CANCELLED')),
  notes                text NOT NULL DEFAULT '',
  created_by_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  created_by           text NOT NULL DEFAULT '',
  confirmed_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  confirmed_by         text,
  confirmed_at         timestamptz,
  cancelled_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  cancelled_by         text,
  cancelled_at         timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (promised_date IS NULL OR promised_date >= order_date),
  CHECK ((status='CONFIRMED' OR status='PARTIALLY_SHIPPED' OR status='SHIPPED') = (confirmed_at IS NOT NULL)
         OR status='CANCELLED')
);
CREATE INDEX IF NOT EXISTS sales_orders_customer_idx ON sales_orders(customer_id, order_date DESC);
CREATE INDEX IF NOT EXISTS sales_orders_status_idx ON sales_orders(status, promised_date);

CREATE TABLE IF NOT EXISTS sales_order_lines (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sales_order_id  uuid NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
  line_no         integer NOT NULL CHECK (line_no > 0),
  item_id         uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  quantity        numeric NOT NULL CHECK (quantity > 0),
  allocated_qty   numeric NOT NULL DEFAULT 0 CHECK (allocated_qty >= 0),
  shipped_qty     numeric NOT NULL DEFAULT 0 CHECK (shipped_qty >= 0),
  cancelled_qty   numeric NOT NULL DEFAULT 0 CHECK (cancelled_qty >= 0),
  unit_price      numeric NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
  requested_date  date NOT NULL,
  promised_date   date,
  notes           text NOT NULL DEFAULT '',
  UNIQUE(sales_order_id, line_no),
  CHECK (promised_date IS NULL OR promised_date >= requested_date),
  CHECK (allocated_qty + shipped_qty + cancelled_qty <= quantity + 0.000001)
);
CREATE INDEX IF NOT EXISTS sales_order_lines_item_promise_idx ON sales_order_lines(item_id, promised_date);
CREATE INDEX IF NOT EXISTS sales_order_lines_order_idx ON sales_order_lines(sales_order_id, line_no);

CREATE TABLE IF NOT EXISTS sales_order_status_history (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sales_order_id uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  from_status    text,
  to_status      text NOT NULL,
  actor_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
  actor_username text NOT NULL,
  occurred_at    timestamptz NOT NULL DEFAULT now(),
  source         text NOT NULL DEFAULT 'API'
);
CREATE INDEX IF NOT EXISTS sales_order_status_history_idx ON sales_order_status_history(sales_order_id, occurred_at, id);

CREATE TABLE IF NOT EXISTS sales_order_allocation_events (
  id                 uuid PRIMARY KEY,
  sales_order_line_id uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  event_type          text NOT NULL CHECK (event_type IN ('ALLOCATE','RELEASE','SHIP_RELEASE','CANCEL_RELEASE')),
  quantity            numeric NOT NULL CHECK (quantity > 0),
  inventory_txn_id    uuid NOT NULL UNIQUE REFERENCES inventory_txns(id) ON DELETE RESTRICT,
  actor_user_id       uuid REFERENCES users(id) ON DELETE SET NULL,
  actor_username      text NOT NULL,
  occurred_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sales_order_alloc_line_idx ON sales_order_allocation_events(sales_order_line_id, occurred_at, id);

CREATE TABLE IF NOT EXISTS sales_order_shipments (
  id                   uuid PRIMARY KEY,
  sales_order_id       uuid NOT NULL REFERENCES sales_orders(id) ON DELETE RESTRICT,
  sales_order_line_id  uuid NOT NULL REFERENCES sales_order_lines(id) ON DELETE RESTRICT,
  quantity             numeric NOT NULL CHECK (quantity > 0),
  inventory_txn_id     uuid NOT NULL UNIQUE REFERENCES inventory_txns(id) ON DELETE RESTRICT,
  shipped_at           timestamptz NOT NULL DEFAULT now(),
  shipped_by_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  shipped_by_username  text NOT NULL,
  carrier              text NOT NULL DEFAULT '',
  tracking_no          text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sales_order_shipments_order_idx ON sales_order_shipments(sales_order_id, shipped_at, id);
CREATE INDEX IF NOT EXISTS sales_order_shipments_line_idx ON sales_order_shipments(sales_order_line_id, shipped_at, id);

-- Legacy committed demand is reconstructed exactly once into confirmed sales orders.
DO $$
DECLARE
  legacy_customer uuid;
  r record;
  so_id uuid;
BEGIN
  INSERT INTO customers(customer_no,name,status,notes,created_by)
  VALUES ('LEGACY','Legacy migrated demand','ACTIVE','Reconstructed from demand_forecasts(source=ORDER) by migration 0031','LEGACY_MIGRATION')
  ON CONFLICT (customer_no) DO UPDATE SET name=EXCLUDED.name
  RETURNING id INTO legacy_customer;
  IF legacy_customer IS NULL THEN
    SELECT id INTO legacy_customer FROM customers WHERE customer_no='LEGACY';
  END IF;

  -- Legacy rows can contain overdue demand whose due_date predates created_at::date.
  -- Normalize only the Sales Order business order_date so promised_date >= order_date,
  -- while preserving the original created_at/confirmed_at audit timestamps below.
  FOR r IN SELECT * FROM demand_forecasts WHERE source='ORDER' ORDER BY created_at,id LOOP
    IF NOT EXISTS (SELECT 1 FROM sales_orders WHERE order_no='LEGACY-'||replace(left(r.id::text,12),'-','')) THEN
      so_id := gen_random_uuid();
      INSERT INTO sales_orders(
        id,order_no,customer_id,order_date,requested_date,promised_date,status,notes,
        created_by,confirmed_by,confirmed_at,created_at,updated_at
      ) VALUES (
        so_id,'LEGACY-'||replace(left(r.id::text,12),'-',''),legacy_customer,LEAST(r.created_at::date,r.due_date),r.due_date,r.due_date,
        'CONFIRMED','Legacy committed demand migrated from demand_forecasts','LEGACY_MIGRATION','LEGACY_MIGRATION',r.created_at,r.created_at,r.created_at
      );
      INSERT INTO sales_order_lines(id,sales_order_id,line_no,item_id,quantity,requested_date,promised_date,notes)
      VALUES(gen_random_uuid(),so_id,1,r.item_id,r.quantity,r.due_date,r.due_date,'Legacy migrated demand');
      INSERT INTO sales_order_status_history(id,sales_order_id,from_status,to_status,actor_username,occurred_at,source)
      VALUES(gen_random_uuid(),so_id,'DRAFT','CONFIRMED','LEGACY_MIGRATION',r.created_at,'LEGACY_MIGRATION');
    END IF;
  END LOOP;
END$$;

CREATE OR REPLACE FUNCTION guard_sales_order_status_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.status NOT IN ('DRAFT','CONFIRMED') THEN
      RAISE EXCEPTION 'new sales order must be DRAFT (or CONFIRMED only for migration)' USING ERRCODE='23514';
    END IF;
    IF NEW.status='CONFIRMED' AND NEW.created_by <> 'LEGACY_MIGRATION' THEN
      RAISE EXCEPTION 'direct CONFIRMED insert is reserved for legacy migration' USING ERRCODE='23514';
    END IF;
    IF NEW.created_by <> 'LEGACY_MIGRATION' AND EXISTS (SELECT 1 FROM customers c WHERE c.id=NEW.customer_id AND c.status='BLOCKED') THEN
      RAISE EXCEPTION 'BLOCKED customer cannot receive new sales orders' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NEW; END IF;
  IF NEW.status='CONFIRMED' AND EXISTS (SELECT 1 FROM customers c WHERE c.id=NEW.customer_id AND c.status='BLOCKED') THEN
    RAISE EXCEPTION 'BLOCKED customer cannot be confirmed' USING ERRCODE='23514';
  END IF;
  IF NOT (
    (OLD.status='DRAFT' AND NEW.status IN ('CONFIRMED','CANCELLED')) OR
    (OLD.status='CONFIRMED' AND NEW.status IN ('PARTIALLY_SHIPPED','SHIPPED','CANCELLED')) OR
    (OLD.status='PARTIALLY_SHIPPED' AND NEW.status IN ('PARTIALLY_SHIPPED','SHIPPED','CANCELLED'))
  ) THEN
    RAISE EXCEPTION 'invalid sales order transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
  END IF;
  IF OLD.status IN ('SHIPPED','CANCELLED') THEN
    RAISE EXCEPTION 'terminal sales order % is immutable', OLD.id USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS sales_order_status_transition_trg ON sales_orders;
CREATE TRIGGER sales_order_status_transition_trg
BEFORE INSERT OR UPDATE OF status ON sales_orders
FOR EACH ROW EXECUTE FUNCTION guard_sales_order_status_transition();

CREATE OR REPLACE FUNCTION guard_sales_order_header_master_edit()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' AND (
     NEW.order_no IS DISTINCT FROM OLD.order_no OR
     NEW.customer_id IS DISTINCT FROM OLD.customer_id OR
     NEW.order_date IS DISTINCT FROM OLD.order_date OR
     NEW.requested_date IS DISTINCT FROM OLD.requested_date OR
     NEW.promised_date IS DISTINCT FROM OLD.promised_date) THEN
    RAISE EXCEPTION 'confirmed sales order business fields are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS sales_order_header_master_edit_trg ON sales_orders;
CREATE TRIGGER sales_order_header_master_edit_trg
BEFORE UPDATE ON sales_orders
FOR EACH ROW EXECUTE FUNCTION guard_sales_order_header_master_edit();

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
      NEW.promised_date IS DISTINCT FROM OLD.promised_date OR NEW.line_no IS DISTINCT FROM OLD.line_no OR
      NEW.sales_order_id IS DISTINCT FROM OLD.sales_order_id) THEN
    RAISE EXCEPTION 'confirmed sales order business fields are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS sales_order_line_master_edit_trg ON sales_order_lines;
CREATE TRIGGER sales_order_line_master_edit_trg
BEFORE INSERT OR UPDATE OR DELETE ON sales_order_lines
FOR EACH ROW EXECUTE FUNCTION guard_sales_order_line_master_edit();

CREATE OR REPLACE FUNCTION reject_sales_order_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE='23514';
END$$;
DO $$ BEGIN
  DROP TRIGGER IF EXISTS sales_order_history_append_only_trg ON sales_order_status_history;
  CREATE TRIGGER sales_order_history_append_only_trg BEFORE UPDATE OR DELETE ON sales_order_status_history
    FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
  DROP TRIGGER IF EXISTS sales_order_alloc_append_only_trg ON sales_order_allocation_events;
  CREATE TRIGGER sales_order_alloc_append_only_trg BEFORE UPDATE OR DELETE ON sales_order_allocation_events
    FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
  DROP TRIGGER IF EXISTS sales_order_ship_append_only_trg ON sales_order_shipments;
  CREATE TRIGGER sales_order_ship_append_only_trg BEFORE UPDATE OR DELETE ON sales_order_shipments
    FOR EACH ROW EXECUTE FUNCTION reject_sales_order_evidence_mutation();
END$$;

CREATE OR REPLACE FUNCTION assert_sales_order_line_reconciled(p_line uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE l sales_order_lines%ROWTYPE; alloc numeric; shipped numeric;
BEGIN
  SELECT * INTO l FROM sales_order_lines WHERE id=p_line;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT COALESCE(SUM(CASE WHEN event_type='ALLOCATE' THEN quantity ELSE -quantity END),0)
    INTO alloc FROM sales_order_allocation_events WHERE sales_order_line_id=p_line;
  SELECT COALESCE(SUM(quantity),0) INTO shipped FROM sales_order_shipments WHERE sales_order_line_id=p_line;
  IF abs(l.allocated_qty-alloc) > 0.000001 THEN
    RAISE EXCEPTION 'sales order line % allocation mismatch: line %, events %',p_line,l.allocated_qty,alloc USING ERRCODE='23514';
  END IF;
  IF abs(l.shipped_qty-shipped) > 0.000001 THEN
    RAISE EXCEPTION 'sales order line % shipment mismatch: line %, shipments %',p_line,l.shipped_qty,shipped USING ERRCODE='23514';
  END IF;
  IF l.allocated_qty < -0.000001 OR l.shipped_qty < -0.000001 OR l.cancelled_qty < -0.000001 OR
     l.allocated_qty+l.shipped_qty+l.cancelled_qty > l.quantity+0.000001 THEN
    RAISE EXCEPTION 'sales order line % quantities are inconsistent',p_line USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION deferred_sales_order_line_reconcile_line()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM assert_sales_order_line_reconciled(NEW.id);
  RETURN NEW;
END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_line_reconcile_alloc()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM assert_sales_order_line_reconciled(NEW.sales_order_line_id);
  RETURN NEW;
END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_line_reconcile_ship()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM assert_sales_order_line_reconciled(NEW.sales_order_line_id);
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS sales_order_line_reconcile_line_trg ON sales_order_lines;
CREATE CONSTRAINT TRIGGER sales_order_line_reconcile_line_trg AFTER INSERT OR UPDATE ON sales_order_lines
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_line_reconcile_line();
DROP TRIGGER IF EXISTS sales_order_line_reconcile_alloc_trg ON sales_order_allocation_events;
CREATE CONSTRAINT TRIGGER sales_order_line_reconcile_alloc_trg AFTER INSERT ON sales_order_allocation_events
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_line_reconcile_alloc();
DROP TRIGGER IF EXISTS sales_order_line_reconcile_ship_trg ON sales_order_shipments;
CREATE CONSTRAINT TRIGGER sales_order_line_reconcile_ship_trg AFTER INSERT ON sales_order_shipments
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_line_reconcile_ship();

CREATE OR REPLACE FUNCTION assert_sales_order_shipment_ledger(p_shipment uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE sh sales_order_shipments%ROWTYPE; ln sales_order_lines%ROWTYPE; tx inventory_txns%ROWTYPE;
BEGIN
  SELECT * INTO sh FROM sales_order_shipments WHERE id=p_shipment;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT * INTO ln FROM sales_order_lines WHERE id=sh.sales_order_line_id;
  IF ln.id IS NULL OR ln.sales_order_id <> sh.sales_order_id THEN
    RAISE EXCEPTION 'sales order shipment % order/line mismatch',p_shipment USING ERRCODE='23514';
  END IF;
  SELECT * INTO tx FROM inventory_txns WHERE id=sh.inventory_txn_id;
  IF tx.id IS NULL OR tx.txn_type <> 'ISSUE' OR tx.item_id <> ln.item_id OR abs(tx.quantity + sh.quantity) > 0.000001 THEN
    RAISE EXCEPTION 'sales order shipment % inventory ledger mismatch',p_shipment USING ERRCODE='23514';
  END IF;
  IF tx.ref_doc <> 'SO:'||(SELECT order_no FROM sales_orders WHERE id=sh.sales_order_id)||':SHIP:'||sh.id::text THEN
    RAISE EXCEPTION 'sales order shipment % ref_doc mismatch',p_shipment USING ERRCODE='23514';
  END IF;
END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_shipment_ledger()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_shipment_ledger(NEW.id); RETURN NEW; END$$;
DROP TRIGGER IF EXISTS sales_order_shipment_ledger_trg ON sales_order_shipments;
CREATE CONSTRAINT TRIGGER sales_order_shipment_ledger_trg AFTER INSERT ON sales_order_shipments
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_shipment_ledger();

CREATE OR REPLACE FUNCTION assert_sales_order_allocation_ledger(p_event uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE ev sales_order_allocation_events%ROWTYPE; ln sales_order_lines%ROWTYPE; so sales_orders%ROWTYPE; tx inventory_txns%ROWTYPE; expected_type text;
BEGIN
  SELECT * INTO ev FROM sales_order_allocation_events WHERE id=p_event;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT * INTO ln FROM sales_order_lines WHERE id=ev.sales_order_line_id;
  SELECT * INTO so FROM sales_orders WHERE id=ln.sales_order_id;
  SELECT * INTO tx FROM inventory_txns WHERE id=ev.inventory_txn_id;
  expected_type := CASE WHEN ev.event_type='ALLOCATE' THEN 'RESERVE' ELSE 'UNRESERVE' END;
  IF tx.id IS NULL OR tx.txn_type <> expected_type OR tx.item_id <> ln.item_id OR abs(abs(tx.quantity)-ev.quantity) > 0.000001 THEN
    RAISE EXCEPTION 'sales order allocation event % inventory ledger mismatch',p_event USING ERRCODE='23514';
  END IF;
  IF tx.ref_doc <> 'SO:'||so.order_no||':LINE:'||ln.id::text THEN
    RAISE EXCEPTION 'sales order allocation event % ref_doc mismatch',p_event USING ERRCODE='23514';
  END IF;
END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_allocation_ledger()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_allocation_ledger(NEW.id); RETURN NEW; END$$;
DROP TRIGGER IF EXISTS sales_order_allocation_ledger_trg ON sales_order_allocation_events;
CREATE CONSTRAINT TRIGGER sales_order_allocation_ledger_trg AFTER INSERT ON sales_order_allocation_events
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_allocation_ledger();

CREATE OR REPLACE FUNCTION guard_linked_sales_order_inventory_txn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_id uuid;
BEGIN
  v_id := OLD.id;
  IF EXISTS (SELECT 1 FROM sales_order_allocation_events WHERE inventory_txn_id=v_id)
     OR EXISTS (SELECT 1 FROM sales_order_shipments WHERE inventory_txn_id=v_id) THEN
    RAISE EXCEPTION 'inventory transaction % is bound to Sales Order evidence and is immutable',v_id USING ERRCODE='23514';
  END IF;
  RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END$$;
DROP TRIGGER IF EXISTS no_mutate_sales_order_inventory_txn_trg ON inventory_txns;
CREATE TRIGGER no_mutate_sales_order_inventory_txn_trg
BEFORE UPDATE OR DELETE ON inventory_txns
FOR EACH ROW EXECUTE FUNCTION guard_linked_sales_order_inventory_txn();

CREATE OR REPLACE FUNCTION assert_sales_order_header_reconciled(p_order uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE s text; open_q numeric; ship_q numeric; alloc_q numeric;
BEGIN
  SELECT status INTO s FROM sales_orders WHERE id=p_order;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT COALESCE(SUM(GREATEST(quantity-shipped_qty-cancelled_qty,0)),0),COALESCE(SUM(shipped_qty),0),COALESCE(SUM(allocated_qty),0)
    INTO open_q,ship_q,alloc_q FROM sales_order_lines WHERE sales_order_id=p_order;
  IF s='DRAFT' AND (ship_q<>0 OR alloc_q<>0) THEN RAISE EXCEPTION 'DRAFT sales order % cannot have shipment/allocation',p_order USING ERRCODE='23514'; END IF;
  IF s='CONFIRMED' AND ship_q>0.000001 THEN RAISE EXCEPTION 'CONFIRMED sales order % cannot already be shipped',p_order USING ERRCODE='23514'; END IF;
  IF s='PARTIALLY_SHIPPED' AND NOT (ship_q>0.000001 AND open_q>0.000001) THEN RAISE EXCEPTION 'PARTIALLY_SHIPPED sales order % status mismatch',p_order USING ERRCODE='23514'; END IF;
  IF s='SHIPPED' AND NOT (ship_q>0.000001 AND open_q<=0.000001 AND alloc_q<=0.000001) THEN RAISE EXCEPTION 'SHIPPED sales order % status mismatch',p_order USING ERRCODE='23514'; END IF;
  IF s='CANCELLED' AND (open_q>0.000001 OR alloc_q>0.000001) THEN RAISE EXCEPTION 'CANCELLED sales order % must have zero open/allocation',p_order USING ERRCODE='23514'; END IF;
END$$;
CREATE OR REPLACE FUNCTION assert_sales_order_status_history(p_order uuid)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE so sales_orders%ROWTYPE; h sales_order_status_history%ROWTYPE;
BEGIN
  SELECT * INTO so FROM sales_orders WHERE id=p_order;
  IF NOT FOUND THEN RETURN; END IF;
  IF so.status IN ('CONFIRMED','PARTIALLY_SHIPPED','SHIPPED') AND
     (so.confirmed_at IS NULL OR COALESCE(so.confirmed_by,'')='') THEN
    RAISE EXCEPTION 'sales order % confirmed audit is incomplete',p_order USING ERRCODE='23514';
  END IF;
  IF so.status='CANCELLED' AND (so.cancelled_at IS NULL OR COALESCE(so.cancelled_by,'')='') THEN
    RAISE EXCEPTION 'sales order % cancellation audit is incomplete',p_order USING ERRCODE='23514';
  END IF;
  IF so.status <> 'DRAFT' THEN
    SELECT * INTO h FROM sales_order_status_history WHERE sales_order_id=p_order ORDER BY occurred_at DESC,id DESC LIMIT 1;
    IF h.id IS NULL OR h.to_status <> so.status THEN
      RAISE EXCEPTION 'sales order % status/history mismatch: header %, history %',p_order,so.status,COALESCE(h.to_status,'NONE') USING ERRCODE='23514';
    END IF;
  END IF;
END$$;

CREATE OR REPLACE FUNCTION deferred_sales_order_status_audit_from_order()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_status_history(NEW.id); RETURN NEW; END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_status_audit_from_history()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_status_history(NEW.sales_order_id); RETURN NEW; END$$;

CREATE OR REPLACE FUNCTION deferred_sales_order_header_from_order()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_header_reconciled(NEW.id); RETURN NEW; END$$;
CREATE OR REPLACE FUNCTION deferred_sales_order_header_from_line()
RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM assert_sales_order_header_reconciled(NEW.sales_order_id); RETURN NEW; END$$;
DROP TRIGGER IF EXISTS sales_order_header_reconcile_order_trg ON sales_orders;
CREATE CONSTRAINT TRIGGER sales_order_header_reconcile_order_trg AFTER INSERT OR UPDATE ON sales_orders
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_header_from_order();
DROP TRIGGER IF EXISTS sales_order_header_reconcile_line_trg ON sales_order_lines;
CREATE CONSTRAINT TRIGGER sales_order_header_reconcile_line_trg AFTER INSERT OR UPDATE ON sales_order_lines
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_header_from_line();
DROP TRIGGER IF EXISTS sales_order_status_audit_order_trg ON sales_orders;
CREATE CONSTRAINT TRIGGER sales_order_status_audit_order_trg AFTER INSERT OR UPDATE OF status,confirmed_at,confirmed_by,cancelled_at,cancelled_by ON sales_orders
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_status_audit_from_order();
DROP TRIGGER IF EXISTS sales_order_status_audit_history_trg ON sales_order_status_history;
CREATE CONSTRAINT TRIGGER sales_order_status_audit_history_trg AFTER INSERT ON sales_order_status_history
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION deferred_sales_order_status_audit_from_history();

CREATE OR REPLACE FUNCTION forbid_legacy_demand_forecast_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'demand_forecasts is legacy read-only after 0031; use Sales Orders and forecast_runs' USING ERRCODE='23514';
END$$;
DROP TRIGGER IF EXISTS demand_forecasts_legacy_read_only_trg ON demand_forecasts;
CREATE TRIGGER demand_forecasts_legacy_read_only_trg
BEFORE INSERT OR UPDATE OR DELETE ON demand_forecasts
FOR EACH ROW EXECUTE FUNCTION forbid_legacy_demand_forecast_mutation();

CREATE OR REPLACE VIEW v_sales_order_open_demand AS
SELECT so.id AS sales_order_id, so.order_no, so.customer_id, c.customer_no, c.name AS customer_name,
       l.id AS sales_order_line_id, l.item_id,
       COALESCE(l.promised_date,so.promised_date,l.requested_date,so.requested_date) AS demand_date,
       GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0) AS open_qty,
       l.allocated_qty, l.shipped_qty, l.cancelled_qty
  FROM sales_orders so
  JOIN customers c ON c.id=so.customer_id
  JOIN sales_order_lines l ON l.sales_order_id=so.id
 WHERE so.status IN ('CONFIRMED','PARTIALLY_SHIPPED')
   AND GREATEST(l.quantity-l.shipped_qty-l.cancelled_qty,0) > 0;

COMMENT ON VIEW v_sales_order_open_demand IS 'Canonical committed open customer demand for ATP and forecast consumption';
