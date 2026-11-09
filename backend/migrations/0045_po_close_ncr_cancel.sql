-- 0045: closing purchase orders and cancelling supplier NCRs.
--
-- purchase_orders.status='CLOSED' and supplier_ncrs.status='CANCELLED' have
-- existed since 0022 and 0027, but nothing recorded who did it or why.
--
-- A closed PO is final: its remaining quantity stops counting as supply and it
-- cannot be received or reopened. Cancelling an NCR does not change the lot's
-- quality status, because USE_AS_IS is the admin-only way to release a lot
-- without inspection. The lot returns to OK through a PASS inspection.

ALTER TABLE purchase_orders
  ADD COLUMN closed_at         timestamptz,
  ADD COLUMN closed_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN closed_by         text NOT NULL DEFAULT '',
  ADD COLUMN close_reason      text NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION guard_purchase_order_close()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_user users%ROWTYPE;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status = 'CLOSED' THEN
      RAISE EXCEPTION 'a purchase order cannot be created CLOSED' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;

  IF OLD.status = 'CLOSED' THEN
    IF NEW.status <> 'CLOSED'
       OR NEW.quantity IS DISTINCT FROM OLD.quantity
       OR NEW.received_qty IS DISTINCT FROM OLD.received_qty
       OR NEW.closed_at IS DISTINCT FROM OLD.closed_at
       OR NEW.closed_by_user_id IS DISTINCT FROM OLD.closed_by_user_id
       OR NEW.closed_by IS DISTINCT FROM OLD.closed_by
       OR NEW.close_reason IS DISTINCT FROM OLD.close_reason THEN
      RAISE EXCEPTION 'closed purchase order % is final', OLD.po_no USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.status = 'CLOSED' THEN
    IF OLD.status NOT IN ('OPEN','PARTIALLY_RECEIVED') THEN
      RAISE EXCEPTION 'purchase order in status % cannot be closed', OLD.status USING ERRCODE='23514';
    END IF;
    IF btrim(NEW.close_reason) = '' THEN
      RAISE EXCEPTION 'closing a purchase order requires a reason' USING ERRCODE='23514';
    END IF;
    IF NEW.closed_by_user_id IS NULL THEN
      RAISE EXCEPTION 'closing a purchase order requires an actor' USING ERRCODE='23514';
    END IF;
    SELECT * INTO v_user FROM users WHERE id = NEW.closed_by_user_id;
    IF NOT FOUND OR NOT v_user.is_active OR v_user.role NOT IN ('planner','admin') THEN
      RAISE EXCEPTION 'purchase order closer must be active planner/admin' USING ERRCODE='23514';
    END IF;
    NEW.closed_by := v_user.username;
    NEW.closed_at := transaction_timestamp();
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS guard_purchase_order_close_trg ON purchase_orders;
CREATE TRIGGER guard_purchase_order_close_trg
BEFORE INSERT OR UPDATE ON purchase_orders
FOR EACH ROW EXECUTE FUNCTION guard_purchase_order_close();

CREATE OR REPLACE FUNCTION guard_closed_purchase_order_receipt()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM purchase_orders WHERE id = NEW.purchase_order_id AND status = 'CLOSED') THEN
    RAISE EXCEPTION 'closed purchase order cannot be received' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS guard_closed_purchase_order_receipt_trg ON purchase_receipts;
CREATE TRIGGER guard_closed_purchase_order_receipt_trg
BEFORE INSERT ON purchase_receipts
FOR EACH ROW EXECUTE FUNCTION guard_closed_purchase_order_receipt();

ALTER TABLE supplier_ncrs ADD COLUMN cancel_reason text NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION guard_supplier_ncr_cancel_reason()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'CANCELLED' AND OLD.status <> 'CANCELLED' THEN
    IF btrim(NEW.cancel_reason) = '' THEN
      RAISE EXCEPTION 'cancelling an NCR requires a reason' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.cancel_reason IS DISTINCT FROM OLD.cancel_reason THEN
    RAISE EXCEPTION 'NCR cancel reason can only be set when cancelling' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS guard_supplier_ncr_cancel_reason_trg ON supplier_ncrs;
CREATE TRIGGER guard_supplier_ncr_cancel_reason_trg
BEFORE UPDATE ON supplier_ncrs
FOR EACH ROW EXECUTE FUNCTION guard_supplier_ncr_cancel_reason();

-- Same as 0027, with the cancel reason written to the NCR history.
CREATE OR REPLACE FUNCTION supplier_ncr_after_update()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_disp text;
  v_disp_actor_id uuid;
  v_disp_actor text;
  v_event text;
BEGIN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    SELECT disposition, decided_by_user_id, decided_by
      INTO v_disp, v_disp_actor_id, v_disp_actor
      FROM supplier_ncr_dispositions WHERE ncr_id=NEW.id;

    IF OLD.status='OPEN' AND v_disp IS NOT NULL THEN
      v_event := 'DISPOSITION_' || v_disp;
    ELSIF NEW.status='CANCELLED' THEN
      v_event := 'CANCELLED';
    ELSIF NEW.status='CLOSED' THEN
      v_event := 'CLOSED';
    ELSE
      v_event := 'STATUS_CHANGED';
    END IF;

    INSERT INTO supplier_ncr_history(ncr_id, from_status, to_status, event_type,
      actor_user_id, actor, occurred_at, notes)
    VALUES (
      NEW.id, OLD.status, NEW.status, v_event,
      COALESCE(NEW.closed_by_user_id, v_disp_actor_id),
      COALESCE(NULLIF(NEW.closed_by,''), v_disp_actor, ''),
      transaction_timestamp(), CASE WHEN NEW.status='CANCELLED' THEN NEW.cancel_reason ELSE '' END
    );
  END IF;
  RETURN NEW;
END$$;

-- Same as 0027, except that cancelled NCRs no longer count against the supplier.
CREATE OR REPLACE VIEW v_supplier_quality_scorecard AS
WITH suppliers AS (
  SELECT supplier_name AS supplier FROM supplier_quality_profiles
  UNION SELECT DISTINCT btrim(supplier) FROM purchase_orders WHERE btrim(supplier)<>''
  UNION SELECT DISTINCT btrim(supplier) FROM lots WHERE btrim(supplier)<>''
), receipts AS (
  SELECT btrim(po.supplier) supplier, count(*) receipt_count, COALESCE(sum(pr.quantity),0) received_qty
    FROM purchase_receipts pr JOIN purchase_orders po ON po.id=pr.purchase_order_id
   GROUP BY btrim(po.supplier)
), inspections AS (
  SELECT btrim(l.supplier) supplier,
         count(*) inspection_count,
         count(*) FILTER (WHERE qi.result='FAIL') fail_inspection_count,
         count(DISTINCT qi.lot_id) FILTER (WHERE qi.result='FAIL') rejected_lot_count,
         COALESCE(sum(qi.defect_qty),0) defect_qty
    FROM quality_inspections qi JOIN lots l ON l.id=qi.lot_id
   WHERE btrim(l.supplier)<>''
   GROUP BY btrim(l.supplier)
), ncr AS (
  SELECT btrim(supplier) supplier,
         count(*) ncr_count,
         count(*) FILTER (WHERE status IN ('OPEN','IN_REWORK')) open_ncr_count,
         count(*) FILTER (WHERE severity='CRITICAL') critical_ncr_count
    FROM supplier_ncrs WHERE status <> 'CANCELLED' GROUP BY btrim(supplier)
), disp AS (
  SELECT btrim(n.supplier) supplier,
         COALESCE(sum(d.quantity) FILTER (WHERE d.disposition='RETURN_TO_SUPPLIER'),0) returned_qty,
         COALESCE(sum(d.quantity) FILTER (WHERE d.disposition='SCRAP'),0) scrapped_qty
    FROM supplier_ncr_dispositions d JOIN supplier_ncrs n ON n.id=d.ncr_id
   GROUP BY btrim(n.supplier)
)
SELECT s.supplier,
       COALESCE(p.status,'APPROVED') profile_status,
       COALESCE(p.inspection_required,false) inspection_required,
       COALESCE(p.target_ppm,0) target_ppm,
       COALESCE(r.receipt_count,0) receipt_count,
       COALESCE(r.received_qty,0) received_qty,
       COALESCE(i.inspection_count,0) inspection_count,
       COALESCE(i.fail_inspection_count,0) fail_inspection_count,
       COALESCE(i.rejected_lot_count,0) rejected_lot_count,
       COALESCE(i.defect_qty,0) defect_qty,
       COALESCE(n.ncr_count,0) ncr_count,
       COALESCE(n.open_ncr_count,0) open_ncr_count,
       COALESCE(n.critical_ncr_count,0) critical_ncr_count,
       COALESCE(d.returned_qty,0) returned_qty,
       COALESCE(d.scrapped_qty,0) scrapped_qty,
       CASE WHEN COALESCE(r.received_qty,0)>0
            THEN COALESCE(i.defect_qty,0) / r.received_qty * 1000000.0
            ELSE 0 END AS defect_ppm
  FROM suppliers s
  LEFT JOIN supplier_quality_profiles p ON p.supplier_name=s.supplier
  LEFT JOIN receipts r ON r.supplier=s.supplier
  LEFT JOIN inspections i ON i.supplier=s.supplier
  LEFT JOIN ncr n ON n.supplier=s.supplier
  LEFT JOIN disp d ON d.supplier=s.supplier;
