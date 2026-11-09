-- 0046: record why a purchase order was closed.
--
-- SUPPLIER closures count against the supplier's on-time rate; INTERNAL ones
-- (demand went away, design change) do not. Orders closed before this migration
-- have no recorded cause, so they become UNKNOWN and stay out of the statistics.

ALTER TABLE purchase_orders
  ADD COLUMN close_cause text NOT NULL DEFAULT ''
  CHECK (close_cause IN ('','SUPPLIER','INTERNAL','UNKNOWN'));

UPDATE purchase_orders SET close_cause='UNKNOWN' WHERE status='CLOSED';

-- Same as 0045, plus the cause.
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
       OR NEW.close_reason IS DISTINCT FROM OLD.close_reason
       OR NEW.close_cause IS DISTINCT FROM OLD.close_cause THEN
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
    IF NEW.close_cause NOT IN ('SUPPLIER','INTERNAL') THEN
      RAISE EXCEPTION 'closing a purchase order requires a cause (SUPPLIER or INTERNAL)' USING ERRCODE='23514';
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
