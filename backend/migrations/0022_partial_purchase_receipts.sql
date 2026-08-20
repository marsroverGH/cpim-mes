-- ============================================================================
-- 0022: Partial Purchase Receipts + idempotency + planning remaining quantity
--
-- Invariants after this migration:
--   * purchase_receipts is the immutable event history for PO receipts.
--   * purchase_orders.received_qty = SUM(purchase_receipts.quantity).
--   * OPEN/PARTIALLY_RECEIVED/RECEIVED status follows the cumulative quantity.
--   * received_qty never exceeds ordered quantity.
--   * Every API receipt is linked 1:1 to the exact unified inventory RECEIPT and lot.
--   * A PO receipt inventory transaction cannot exist without purchase_receipts.
--   * Bound receipt history / inventory / lot allocations are immutable.
-- ============================================================================

BEGIN;

ALTER TABLE purchase_orders
  ADD COLUMN IF NOT EXISTS received_qty numeric NOT NULL DEFAULT 0;

ALTER TABLE purchase_orders
  DROP CONSTRAINT IF EXISTS purchase_orders_status_check;
ALTER TABLE purchase_orders
  ADD CONSTRAINT purchase_orders_status_check
  CHECK (status IN ('OPEN','PARTIALLY_RECEIVED','RECEIVED','CLOSED'));

-- Existing versions had only full receipt. Preserve that history as cumulative state.
UPDATE purchase_orders
   SET received_qty = CASE
     WHEN status='RECEIVED' OR received_at IS NOT NULL THEN quantity
     ELSE 0
   END;

ALTER TABLE purchase_orders
  DROP CONSTRAINT IF EXISTS purchase_orders_received_qty_check;
ALTER TABLE purchase_orders
  ADD CONSTRAINT purchase_orders_received_qty_check
  CHECK (received_qty >= 0 AND received_qty <= quantity);

CREATE TABLE IF NOT EXISTS purchase_receipts (
  id                   uuid PRIMARY KEY,
  purchase_order_id    uuid NOT NULL REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  item_id               uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  quantity              numeric NOT NULL CHECK (quantity > 0),
  lot_id                uuid NOT NULL REFERENCES lots(id) ON DELETE RESTRICT,
  inventory_txn_id      uuid NOT NULL UNIQUE REFERENCES inventory_txns(id) ON DELETE RESTRICT,
  received_at           timestamptz NOT NULL DEFAULT now(),
  received_by_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  received_by_username  text NOT NULL,
  source                 text NOT NULL DEFAULT 'API'
                         CHECK (source IN ('API','LEGACY_MIGRATION')),
  created_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS purchase_receipts_po_idx
  ON purchase_receipts(purchase_order_id, received_at, id);

-- Reconstruct pre-0022 full receipts from the unified ledger. The old workflow
-- generated one RECEIPT with ref_doc='PO:<poNo>' and stored received_lot_id.
DO $$
DECLARE
  p record;
  cand_count integer;
  txn_id uuid;
  lot_id uuid;
BEGIN
  FOR p IN
    SELECT * FROM purchase_orders WHERE received_qty > 0
  LOOP
    SELECT COUNT(*) INTO cand_count
      FROM inventory_txns t
      JOIN lot_movements lm ON lm.txn_id=t.id
     WHERE t.item_id=p.item_id
       AND t.txn_type='RECEIPT'
       AND abs(t.quantity-p.received_qty) <= 0.000001
       AND t.ref_doc=('PO:' || p.po_no)
       AND abs(lm.quantity-p.received_qty) <= 0.000001
       AND (p.received_lot_id IS NULL OR lm.lot_id=p.received_lot_id);

    SELECT t.id, lm.lot_id INTO txn_id, lot_id
      FROM inventory_txns t
      JOIN lot_movements lm ON lm.txn_id=t.id
     WHERE t.item_id=p.item_id
       AND t.txn_type='RECEIPT'
       AND abs(t.quantity-p.received_qty) <= 0.000001
       AND t.ref_doc=('PO:' || p.po_no)
       AND abs(lm.quantity-p.received_qty) <= 0.000001
       AND (p.received_lot_id IS NULL OR lm.lot_id=p.received_lot_id)
     ORDER BY t.occurred_at, t.id
     LIMIT 1;

    IF cand_count <> 1 OR txn_id IS NULL OR lot_id IS NULL THEN
      RAISE EXCEPTION
        '0022 cannot reconstruct legacy receipt for PO %: expected exactly one unified receipt, found %',
        p.po_no, cand_count;
    END IF;

    INSERT INTO purchase_receipts(
      id, purchase_order_id, item_id, quantity, lot_id, inventory_txn_id,
      received_at, received_by_user_id, received_by_username, source
    ) VALUES (
      gen_random_uuid(), p.id, p.item_id, p.received_qty, lot_id, txn_id,
      COALESCE(p.received_at, now()), NULL, 'legacy-migration', 'LEGACY_MIGRATION'
    ) ON CONFLICT (inventory_txn_id) DO NOTHING;
  END LOOP;
END$$;

-- Validate one receipt event against its business document and unified stock ledger.
CREATE OR REPLACE FUNCTION enforce_purchase_receipt_link()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  po_no_v text;
  po_item uuid;
  po_qty numeric;
  txn_item uuid;
  txn_qty numeric;
  txn_type_v text;
  txn_ref text;
  lot_item uuid;
  allocated numeric;
  expected_ref text;
  current_username text;
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'purchase_receipts are immutable; post an explicit correction/reversal'
      USING ERRCODE='23514';
  END IF;

  IF TG_OP='UPDATE' THEN
    RAISE EXCEPTION 'purchase_receipts are immutable; post an explicit correction/reversal'
      USING ERRCODE='23514';
  END IF;

  SELECT po_no, item_id, quantity INTO po_no_v, po_item, po_qty
    FROM purchase_orders WHERE id=NEW.purchase_order_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'purchase order % does not exist', NEW.purchase_order_id
      USING ERRCODE='23503';
  END IF;

  IF NEW.item_id <> po_item THEN
    RAISE EXCEPTION 'purchase receipt item % does not match PO item %', NEW.item_id, po_item
      USING ERRCODE='23514';
  END IF;

  SELECT item_id, quantity, txn_type, ref_doc
    INTO txn_item, txn_qty, txn_type_v, txn_ref
    FROM inventory_txns WHERE id=NEW.inventory_txn_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'purchase receipt inventory transaction % does not exist', NEW.inventory_txn_id
      USING ERRCODE='23503';
  END IF;

  IF txn_type_v <> 'RECEIPT' OR txn_item <> po_item OR abs(txn_qty-NEW.quantity) > 0.000001 THEN
    RAISE EXCEPTION
      'purchase receipt % inventory transaction mismatch (type %, item %, qty %)',
      NEW.id, txn_type_v, txn_item, txn_qty USING ERRCODE='23514';
  END IF;

  SELECT item_id INTO lot_item FROM lots WHERE id=NEW.lot_id;
  IF NOT FOUND OR lot_item <> po_item THEN
    RAISE EXCEPTION 'purchase receipt lot % does not belong to PO item %', NEW.lot_id, po_item
      USING ERRCODE='23514';
  END IF;

  SELECT COALESCE(SUM(quantity),0) INTO allocated
    FROM lot_movements
   WHERE txn_id=NEW.inventory_txn_id
     AND lot_id=NEW.lot_id
     AND movement_type='RECEIPT';
  IF abs(allocated-NEW.quantity) > 0.000001 THEN
    RAISE EXCEPTION
      'purchase receipt % lot allocation % does not equal receipt quantity % (allocated %)',
      NEW.id, NEW.lot_id, NEW.quantity, allocated USING ERRCODE='23514';
  END IF;

  IF NEW.source='API' THEN
    expected_ref := 'PO:' || po_no_v || ':RCPT:' || NEW.id::text;
    IF txn_ref <> expected_ref THEN
      RAISE EXCEPTION 'purchase receipt % expected ref %, got %', NEW.id, expected_ref, txn_ref
        USING ERRCODE='23514';
    END IF;
    IF NEW.received_by_user_id IS NULL THEN
      RAISE EXCEPTION 'API purchase receipt % requires received_by_user_id', NEW.id
        USING ERRCODE='23514';
    END IF;
    SELECT username INTO current_username FROM users WHERE id=NEW.received_by_user_id AND is_active=true;
    IF NOT FOUND OR current_username <> NEW.received_by_username THEN
      RAISE EXCEPTION 'purchase receipt actor does not match an active user'
        USING ERRCODE='23514';
    END IF;
  END IF;

  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_purchase_receipt_link ON purchase_receipts;
CREATE TRIGGER trg_purchase_receipt_link
BEFORE INSERT OR UPDATE OR DELETE ON purchase_receipts
FOR EACH ROW EXECUTE FUNCTION enforce_purchase_receipt_link();

-- Aggregate/order-state invariant. CLOSED is allowed with an unreceived remainder
-- because closing can intentionally cancel the remainder; it is not scheduled supply.
CREATE OR REPLACE FUNCTION assert_purchase_order_receipt_state(p_po uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  ordered numeric;
  received numeric;
  status_v text;
  hist numeric;
BEGIN
  IF p_po IS NULL THEN RETURN; END IF;
  SELECT quantity, received_qty, status INTO ordered, received, status_v
    FROM purchase_orders WHERE id=p_po;
  IF NOT FOUND THEN RETURN; END IF;

  SELECT COALESCE(SUM(quantity),0) INTO hist
    FROM purchase_receipts WHERE purchase_order_id=p_po;

  IF abs(received-hist) > 0.000001 THEN
    RAISE EXCEPTION 'PO % received_qty % does not equal receipt history %', p_po, received, hist
      USING ERRCODE='23514';
  END IF;
  IF received < -0.000001 OR received > ordered + 0.000001 THEN
    RAISE EXCEPTION 'PO % received quantity % is outside ordered range 0..%', p_po, received, ordered
      USING ERRCODE='23514';
  END IF;

  IF status_v <> 'CLOSED' THEN
    IF received <= 0.000001 AND status_v <> 'OPEN' THEN
      RAISE EXCEPTION 'PO % with zero receipts must be OPEN, not %', p_po, status_v
        USING ERRCODE='23514';
    ELSIF received > 0.000001 AND received < ordered-0.000001 AND status_v <> 'PARTIALLY_RECEIVED' THEN
      RAISE EXCEPTION 'PO % partial receipt %/% must be PARTIALLY_RECEIVED, not %', p_po, received, ordered, status_v
        USING ERRCODE='23514';
    ELSIF received >= ordered-0.000001 AND status_v <> 'RECEIVED' THEN
      RAISE EXCEPTION 'PO % fully received %/% must be RECEIVED, not %', p_po, received, ordered, status_v
        USING ERRCODE='23514';
    END IF;
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_purchase_order_receipt_state()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_TABLE_NAME='purchase_orders' THEN
    PERFORM assert_purchase_order_receipt_state(COALESCE(NEW.id, OLD.id));
  ELSE
    PERFORM assert_purchase_order_receipt_state(COALESCE(NEW.purchase_order_id, OLD.purchase_order_id));
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_po_receipt_state_on_po ON purchase_orders;
CREATE CONSTRAINT TRIGGER trg_po_receipt_state_on_po
AFTER INSERT OR UPDATE ON purchase_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_purchase_order_receipt_state();

DROP TRIGGER IF EXISTS trg_po_receipt_state_on_receipt ON purchase_receipts;
CREATE CONSTRAINT TRIGGER trg_po_receipt_state_on_receipt
AFTER INSERT OR UPDATE OR DELETE ON purchase_receipts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_purchase_order_receipt_state();

-- Inverse orphan guard: a new PO:<po>:RCPT:<uuid> inventory RECEIPT must be linked
-- by exactly one purchase_receipts row by COMMIT.
CREATE OR REPLACE FUNCTION assert_po_receipt_txn_is_bound(p_txn uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  t_type text;
  t_ref text;
  linked integer;
BEGIN
  SELECT txn_type, ref_doc INTO t_type, t_ref FROM inventory_txns WHERE id=p_txn;
  IF NOT FOUND OR t_type <> 'RECEIPT' OR t_ref NOT LIKE 'PO:%' THEN
    RETURN;
  END IF;
  IF t_ref NOT LIKE 'PO:%:RCPT:%' THEN
    RAISE EXCEPTION 'new PO receipt inventory transaction % must use PO:<po>:RCPT:<receiptId> reference, got %',
      p_txn, t_ref USING ERRCODE='23514';
  END IF;
  SELECT COUNT(*) INTO linked FROM purchase_receipts WHERE inventory_txn_id=p_txn;
  IF linked <> 1 THEN
    RAISE EXCEPTION 'PO receipt inventory transaction % must be linked by exactly one purchase_receipts row (found %)',
      p_txn, linked USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_po_receipt_txn_is_bound()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP <> 'DELETE' THEN
    PERFORM assert_po_receipt_txn_is_bound(NEW.id);
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_po_receipt_txn_is_bound ON inventory_txns;
CREATE CONSTRAINT TRIGGER trg_po_receipt_txn_is_bound
AFTER INSERT OR UPDATE ON inventory_txns
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_po_receipt_txn_is_bound();

-- Once referenced by purchase_receipts, the unified ledger header/allocation is
-- immutable. Corrections must be explicit reversing events, never history edits.
CREATE OR REPLACE FUNCTION prevent_bound_po_receipt_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  txn_to_check uuid;
BEGIN
  IF TG_TABLE_NAME='inventory_txns' THEN
    txn_to_check := OLD.id;
  ELSE
    txn_to_check := OLD.txn_id;
  END IF;
  IF txn_to_check IS NOT NULL AND EXISTS (
    SELECT 1 FROM purchase_receipts WHERE inventory_txn_id=txn_to_check
  ) THEN
    RAISE EXCEPTION 'PO receipt transaction % is immutable; use a reversal workflow', txn_to_check
      USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_no_mutate_bound_po_receipt_txn ON inventory_txns;
CREATE TRIGGER trg_no_mutate_bound_po_receipt_txn
BEFORE UPDATE OR DELETE ON inventory_txns
FOR EACH ROW EXECUTE FUNCTION prevent_bound_po_receipt_mutation();

DROP TRIGGER IF EXISTS trg_no_mutate_bound_po_receipt_lot_mv ON lot_movements;
CREATE TRIGGER trg_no_mutate_bound_po_receipt_lot_mv
BEFORE UPDATE OR DELETE ON lot_movements
FOR EACH ROW EXECUTE FUNCTION prevent_bound_po_receipt_mutation();

-- Final migration validation.
DO $$
DECLARE
  bad record;
  po_row record;
BEGIN
  SELECT id, po_no, quantity, received_qty, status INTO bad
    FROM purchase_orders po
   WHERE received_qty < 0 OR received_qty > quantity
      OR abs(received_qty - COALESCE((SELECT SUM(pr.quantity) FROM purchase_receipts pr WHERE pr.purchase_order_id=po.id),0)) > 0.000001
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION '0022 validation failed for PO % (ordered %, received %, status %)',
      bad.po_no, bad.quantity, bad.received_qty, bad.status;
  END IF;

  FOR po_row IN SELECT id FROM purchase_orders LOOP
    PERFORM assert_purchase_order_receipt_state(po_row.id);
  END LOOP;
END$$;

COMMIT;
