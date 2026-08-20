-- ============================================================================
-- 0018: Unified Inventory / Lot Ledger
--
-- Contract after this migration:
--   * inventory_txns is the canonical item-level transaction header.
--   * Every physical RECEIPT / ISSUE / ADJUST MUST be fully allocated to one or
--     more lot_movements whose signed quantity sum equals inventory_txns.quantity.
--   * Every lot_movement MUST reference exactly one inventory_txn and the lot's
--     item must equal the transaction item.
--   * RESERVE / UNRESERVE are logical item-level operations and MUST NOT have lot
--     movements because they do not move physical stock.
--   * v_stock_balance.on_hand is derived from lot_movements, so the user-visible
--     physical stock is literally the sum of lot balances.
--
-- Legacy repair policy:
--   1) inventory_txns remains canonical: orphan lot-only movements are archived
--      as legacy trace records, then removed from the active physical ledger;
--   2) physical inventory transactions lacking complete lot allocation receive a
--      clearly marked MIGRATION-UNALLOCATED lot allocation.
-- No historical record is silently discarded and canonical on-hand is preserved.
-- ============================================================================

BEGIN;

-- --------------------------------------------------------------------------
-- A. Validate semantics that cannot be repaired safely without user intent.
-- --------------------------------------------------------------------------
DO $$
DECLARE
  bad record;
BEGIN
  SELECT id, txn_type, quantity INTO bad
    FROM inventory_txns
   WHERE quantity = 0
      OR (txn_type = 'RECEIPT' AND quantity < 0)
      OR (txn_type = 'ISSUE'   AND quantity > 0)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot continue: inventory_txn % has invalid sign (type %, qty %)',
      bad.id, bad.txn_type, bad.quantity;
  END IF;

  SELECT lm.id, lm.movement_type, lm.quantity INTO bad
    FROM lot_movements lm
   WHERE lm.quantity = 0
      OR (lm.movement_type IN ('RECEIPT','PRODUCED') AND lm.quantity < 0)
      OR (lm.movement_type IN ('ISSUE','CONSUMED')   AND lm.quantity > 0)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot continue: lot_movement % has invalid sign (type %, qty %)',
      bad.id, bad.movement_type, bad.quantity;
  END IF;
END$$;

-- --------------------------------------------------------------------------
-- B. Archive legacy orphan lot-only movements.
--    inventory_txns is the canonical historic quantity, so an orphan lot movement
--    must not manufacture a second item-level transaction during migration.
-- --------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS legacy_orphan_lot_movements_0018 (
  archived_at  timestamptz NOT NULL DEFAULT now(),
  id           uuid PRIMARY KEY,
  lot_id       uuid NOT NULL,
  quantity     numeric NOT NULL,
  movement_type text NOT NULL,
  ref_doc      text NOT NULL DEFAULT '',
  occurred_at  timestamptz NOT NULL,
  archive_reason text NOT NULL
);

INSERT INTO legacy_orphan_lot_movements_0018(
  id, lot_id, quantity, movement_type, ref_doc, occurred_at, archive_reason
)
SELECT lm.id, lm.lot_id, lm.quantity, lm.movement_type, lm.ref_doc, lm.occurred_at,
       'Pre-0018 lot movement had no inventory_txns parent; archived because inventory_txns is canonical'
  FROM lot_movements lm
 WHERE lm.txn_id IS NULL
ON CONFLICT (id) DO NOTHING;

DELETE FROM lot_movements WHERE txn_id IS NULL;

-- --------------------------------------------------------------------------
-- C. Validate already-linked movement ownership before filling missing amounts.
-- --------------------------------------------------------------------------
DO $$
DECLARE
  bad record;
BEGIN
  SELECT lm.id, lm.txn_id, l.item_id AS lot_item, t.item_id AS txn_item
    INTO bad
    FROM lot_movements lm
    JOIN lots l ON l.id = lm.lot_id
    JOIN inventory_txns t ON t.id = lm.txn_id
   WHERE l.item_id <> t.item_id
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot continue: lot movement % links lot item % to inventory item %',
      bad.id, bad.lot_item, bad.txn_item;
  END IF;

  SELECT lm.id, lm.movement_type, lm.quantity, lm.ref_doc,
         t.txn_type, t.ref_doc AS txn_ref
    INTO bad
    FROM lot_movements lm
    JOIN inventory_txns t ON t.id=lm.txn_id
   WHERE COALESCE(lm.ref_doc,'') <> COALESCE(t.ref_doc,'')
      OR (t.txn_type='RECEIPT' AND (lm.quantity <= 0 OR lm.movement_type NOT IN ('RECEIPT','PRODUCED')))
      OR (t.txn_type='ISSUE'   AND (lm.quantity >= 0 OR lm.movement_type NOT IN ('ISSUE','CONSUMED')))
      OR (t.txn_type='ADJUST'  AND (lm.movement_type <> 'ADJUST' OR lm.quantity * t.quantity <= 0))
      OR t.txn_type IN ('RESERVE','UNRESERVE')
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot continue: lot movement % is incompatible with txn type/ref (% / %)',
      bad.id, bad.txn_type, bad.txn_ref;
  END IF;
END$$;

-- Existing partial allocations may be completed only when the missing delta has
-- the same sign as the canonical transaction. Over-allocation cannot be repaired
-- safely without user intent, so abort instead of inventing a reversing lot line.
DO $$
DECLARE
  bad record;
BEGIN
  SELECT t.id, t.quantity, COALESCE(SUM(lm.quantity),0) AS allocated
    INTO bad
    FROM inventory_txns t
    LEFT JOIN lot_movements lm ON lm.txn_id=t.id
   WHERE t.txn_type IN ('RECEIPT','ISSUE','ADJUST')
   GROUP BY t.id, t.quantity
  HAVING abs(COALESCE(SUM(lm.quantity),0)) > abs(t.quantity) + 0.000001
      OR (COALESCE(SUM(lm.quantity),0) <> 0 AND COALESCE(SUM(lm.quantity),0) * t.quantity < 0)
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot safely repair over/opposite lot allocation for txn %: qty %, allocated %',
      bad.id, bad.quantity, bad.allocated;
  END IF;
END$$;

-- --------------------------------------------------------------------------
-- D. Backfill missing/partial lot allocation on physical inventory txns.
--    Positive missing quantities are placed in a clearly marked HOLD lot.
--    Negative missing quantities are allocated FIFO from existing positive lot
--    balances. We never fabricate a negative-balance lot; if the canonical item
--    ledger says stock was issued but there is not enough lot stock to allocate,
--    migration stops and requires explicit reconciliation.
-- --------------------------------------------------------------------------
DO $$
DECLARE
  r record;
  lb record;
  alloc_sum numeric;
  delta numeric;
  remaining numeric;
  use_qty numeric;
  recon_lot uuid;
  recon_no text;
  mv_type text;
BEGIN
  FOR r IN
    SELECT id, item_id, quantity, txn_type, ref_doc, occurred_at
      FROM inventory_txns
     WHERE txn_type IN ('RECEIPT','ISSUE','ADJUST')
     ORDER BY occurred_at, id
  LOOP
    SELECT COALESCE(SUM(quantity),0) INTO alloc_sum
      FROM lot_movements WHERE txn_id = r.id;
    delta := r.quantity - alloc_sum;

    IF abs(delta) <= 0.000001 THEN
      CONTINUE;
    END IF;

    mv_type := CASE
      WHEN r.txn_type = 'RECEIPT' THEN 'RECEIPT'
      WHEN r.txn_type = 'ISSUE'   THEN 'ISSUE'
      ELSE 'ADJUST'
    END;

    IF delta > 0 THEN
      -- Unknown positive legacy stock is real physical stock according to the
      -- canonical item ledger, but its lot identity is unknown. Keep it visible
      -- in a HOLD reconciliation lot so it cannot be silently consumed as OK stock.
      recon_no := 'MIGRATION-UNALLOCATED-' || substring(r.item_id::text,1,8);

      SELECT id INTO recon_lot
        FROM lots
       WHERE item_id = r.item_id AND lot_no = recon_no;

      IF recon_lot IS NULL THEN
        recon_lot := gen_random_uuid();
        INSERT INTO lots(
          id, item_id, lot_no, quantity, received_at, supplier,
          source_doc, notes, quality_status
        ) VALUES (
          recon_lot, r.item_id, recon_no, delta,
          r.occurred_at, 'LEGACY-MIGRATION', 'MIGRATION-0018',
          'Legacy stock with no reliable lot allocation. Reconcile physically before use.',
          'HOLD'
        );
      ELSE
        UPDATE lots
           SET quantity=quantity+delta,
               quality_status='HOLD',
               notes='Legacy stock with no reliable lot allocation. Reconcile physically before use.'
         WHERE id=recon_lot;
      END IF;

      INSERT INTO lot_movements(
        id, lot_id, txn_id, quantity, movement_type, ref_doc, occurred_at
      ) VALUES (
        gen_random_uuid(), recon_lot, r.id, delta, mv_type,
        r.ref_doc, r.occurred_at
      );
    ELSE
      -- Missing negative allocation: consume current positive lot balances FIFO.
      -- Use all quality states because this is historical reconciliation, not a
      -- new operational ISSUE decision.
      remaining := -delta;
      FOR lb IN
        SELECT l.id,
               COALESCE(SUM(lm.quantity),0) AS balance
          FROM lots l
          LEFT JOIN lot_movements lm ON lm.lot_id=l.id
         WHERE l.item_id=r.item_id
         GROUP BY l.id, l.received_at
        HAVING COALESCE(SUM(lm.quantity),0) > 0.000001
         ORDER BY l.received_at, l.id
      LOOP
        EXIT WHEN remaining <= 0.000001;
        use_qty := LEAST(lb.balance, remaining);
        INSERT INTO lot_movements(
          id, lot_id, txn_id, quantity, movement_type, ref_doc, occurred_at
        ) VALUES (
          gen_random_uuid(), lb.id, r.id, -use_qty, mv_type,
          r.ref_doc, r.occurred_at
        );
        remaining := remaining - use_qty;
      END LOOP;

      IF remaining > 0.000001 THEN
        RAISE EXCEPTION
          '0018 cannot allocate legacy negative txn %: %.6f quantity has no positive lot balance; reconcile legacy inventory before migration',
          r.id, remaining;
      END IF;
    END IF;
  END LOOP;
END$$;

-- Existing legacy allocations that were already fully linked are not rewritten.
-- If they already imply a negative current lot balance, the lot identity itself is
-- ambiguous and must be reconciled explicitly rather than silently reassigned.
DO $$
DECLARE
  bad record;
BEGIN
  SELECT l.id, l.lot_no, i.code AS item_code, COALESCE(SUM(lm.quantity),0) AS balance
    INTO bad
    FROM lots l
    JOIN items i ON i.id=l.item_id
    LEFT JOIN lot_movements lm ON lm.lot_id=l.id
   GROUP BY l.id, l.lot_no, i.code
  HAVING COALESCE(SUM(lm.quantity),0) < -0.000001
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 cannot continue: legacy lot % (% / %) has negative balance %; reconcile lot history first',
      bad.id, bad.item_code, bad.lot_no, bad.balance;
  END IF;
END$$;

-- --------------------------------------------------------------------------
-- E. Make txn linkage mandatory and non-destructive.
-- --------------------------------------------------------------------------
ALTER TABLE lot_movements
  ALTER COLUMN txn_id SET NOT NULL;

ALTER TABLE lot_movements
  DROP CONSTRAINT IF EXISTS lot_movements_txn_id_fkey;
ALTER TABLE lot_movements
  ADD CONSTRAINT lot_movements_txn_id_fkey
  FOREIGN KEY (txn_id) REFERENCES inventory_txns(id) ON DELETE RESTRICT;

ALTER TABLE lot_movements
  DROP CONSTRAINT IF EXISTS lot_movements_lot_id_fkey;
ALTER TABLE lot_movements
  ADD CONSTRAINT lot_movements_lot_id_fkey
  FOREIGN KEY (lot_id) REFERENCES lots(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS lot_mv_txn_idx ON lot_movements(txn_id);

-- Quantity sign semantics at the item ledger layer.
ALTER TABLE inventory_txns
  DROP CONSTRAINT IF EXISTS inventory_txns_quantity_semantics;
ALTER TABLE inventory_txns
  ADD CONSTRAINT inventory_txns_quantity_semantics CHECK (
    quantity <> 0 AND
    (txn_type <> 'RECEIPT' OR quantity > 0) AND
    (txn_type <> 'ISSUE'   OR quantity < 0) AND
    (txn_type <> 'RESERVE' OR quantity > 0) AND
    (txn_type <> 'UNRESERVE' OR quantity > 0)
  );

-- --------------------------------------------------------------------------
-- F. Immediate row-level validation of each lot allocation.
-- --------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION enforce_lot_movement_link()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  lot_item uuid;
  txn_item uuid;
  txn_kind text;
  txn_ref text;
  txn_qty numeric;
BEGIN
  IF NEW.txn_id IS NULL THEN
    RAISE EXCEPTION 'lot movement % must reference inventory_txns', NEW.id
      USING ERRCODE = '23514';
  END IF;

  SELECT item_id INTO lot_item FROM lots WHERE id = NEW.lot_id;
  SELECT item_id, txn_type, ref_doc, quantity INTO txn_item, txn_kind, txn_ref, txn_qty
    FROM inventory_txns WHERE id = NEW.txn_id;

  IF txn_item IS NULL THEN
    RAISE EXCEPTION 'inventory transaction % does not exist', NEW.txn_id
      USING ERRCODE = '23503';
  END IF;
  IF lot_item <> txn_item THEN
    RAISE EXCEPTION 'lot item % does not match inventory transaction item %', lot_item, txn_item
      USING ERRCODE = '23514';
  END IF;
  IF txn_kind IN ('RESERVE','UNRESERVE') THEN
    RAISE EXCEPTION '% transaction % cannot have a physical lot movement', txn_kind, NEW.txn_id
      USING ERRCODE = '23514';
  END IF;
  IF NEW.quantity = 0 THEN
    RAISE EXCEPTION 'lot movement quantity must be non-zero'
      USING ERRCODE = '23514';
  END IF;

  IF txn_kind = 'RECEIPT' AND
     (NEW.quantity <= 0 OR NEW.movement_type NOT IN ('RECEIPT','PRODUCED')) THEN
    RAISE EXCEPTION 'RECEIPT transaction requires positive RECEIPT/PRODUCED lot movement'
      USING ERRCODE = '23514';
  ELSIF txn_kind = 'ISSUE' AND
        (NEW.quantity >= 0 OR NEW.movement_type NOT IN ('ISSUE','CONSUMED')) THEN
    RAISE EXCEPTION 'ISSUE transaction requires negative ISSUE/CONSUMED lot movement'
      USING ERRCODE = '23514';
  ELSIF txn_kind = 'ADJUST' AND
        (NEW.movement_type <> 'ADJUST' OR NEW.quantity * txn_qty <= 0) THEN
    RAISE EXCEPTION 'ADJUST transaction requires same-sign ADJUST lot movement'
      USING ERRCODE = '23514';
  END IF;

  IF COALESCE(NEW.ref_doc,'') <> COALESCE(txn_ref,'') THEN
    RAISE EXCEPTION 'lot movement ref_doc must equal inventory transaction ref_doc'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_lot_movement_link ON lot_movements;
CREATE TRIGGER trg_lot_movement_link
BEFORE INSERT OR UPDATE ON lot_movements
FOR EACH ROW EXECUTE FUNCTION enforce_lot_movement_link();

-- A lot's item identity is immutable once created. Changing it would move the
-- lot balance to another item without changing its inventory transaction headers.
CREATE OR REPLACE FUNCTION prevent_lot_item_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.item_id <> OLD.item_id THEN
    RAISE EXCEPTION 'lot item_id is immutable; create a new lot instead'
      USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_prevent_lot_item_change ON lots;
CREATE TRIGGER trg_prevent_lot_item_change
BEFORE UPDATE OF item_id ON lots
FOR EACH ROW EXECUTE FUNCTION prevent_lot_item_change();

-- New lot metadata may be inserted before its receipt movement inside a transaction,
-- but it may not COMMIT without at least one physical ledger movement.
CREATE OR REPLACE FUNCTION require_lot_ledger_history()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM lot_movements WHERE lot_id=NEW.id) THEN
    RAISE EXCEPTION 'lot % has no inventory ledger movement', NEW.id
      USING ERRCODE='23514';
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_require_lot_ledger_history ON lots;
CREATE CONSTRAINT TRIGGER trg_require_lot_ledger_history
AFTER INSERT ON lots
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_lot_ledger_history();

-- --------------------------------------------------------------------------
-- G. Deferred transaction-level equality check.
--    It runs at COMMIT so code may insert header then allocations in either order.
-- --------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION assert_inventory_txn_lot_balance(p_txn uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  t record;
  allocated numeric;
  invalid_allocations integer;
BEGIN
  SELECT id, item_id, quantity, txn_type, ref_doc INTO t
    FROM inventory_txns WHERE id = p_txn;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  SELECT COALESCE(SUM(quantity),0) INTO allocated
    FROM lot_movements WHERE txn_id = p_txn;

  SELECT COUNT(*) INTO invalid_allocations
    FROM lot_movements lm
    JOIN lots l ON l.id=lm.lot_id
   WHERE lm.txn_id=p_txn
     AND (
       l.item_id <> t.item_id
       OR COALESCE(lm.ref_doc,'') <> COALESCE(t.ref_doc,'')
       OR (t.txn_type='RECEIPT' AND (lm.quantity <= 0 OR lm.movement_type NOT IN ('RECEIPT','PRODUCED')))
       OR (t.txn_type='ISSUE'   AND (lm.quantity >= 0 OR lm.movement_type NOT IN ('ISSUE','CONSUMED')))
       OR (t.txn_type='ADJUST'  AND (lm.movement_type <> 'ADJUST' OR lm.quantity * t.quantity <= 0))
       OR (t.txn_type IN ('RESERVE','UNRESERVE'))
     );
  IF invalid_allocations > 0 THEN
    RAISE EXCEPTION 'inventory transaction % has % invalid lot allocation(s)',
      p_txn, invalid_allocations USING ERRCODE='23514';
  END IF;

  IF t.txn_type IN ('RECEIPT','ISSUE','ADJUST') THEN
    IF abs(t.quantity - allocated) > 0.000001 THEN
      RAISE EXCEPTION
        'inventory transaction % quantity % is not fully lot-allocated (allocated %)',
        p_txn, t.quantity, allocated
        USING ERRCODE = '23514';
    END IF;
  ELSE
    IF abs(allocated) > 0.000001 THEN
      RAISE EXCEPTION
        'logical inventory transaction % (%) must not have lot allocation',
        p_txn, t.txn_type
        USING ERRCODE = '23514';
    END IF;
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_check_inventory_txn_lot_balance()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_TABLE_NAME = 'inventory_txns' THEN
    IF TG_OP <> 'DELETE' THEN
      PERFORM assert_inventory_txn_lot_balance(NEW.id);
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.id <> NEW.id THEN
      PERFORM assert_inventory_txn_lot_balance(OLD.id);
    END IF;
  ELSE
    IF TG_OP = 'INSERT' THEN
      IF NEW.txn_id IS NOT NULL THEN
        PERFORM assert_inventory_txn_lot_balance(NEW.txn_id);
      END IF;
    ELSIF TG_OP = 'UPDATE' THEN
      IF NEW.txn_id IS NOT NULL THEN
        PERFORM assert_inventory_txn_lot_balance(NEW.txn_id);
      END IF;
      IF OLD.txn_id IS NOT NULL AND OLD.txn_id <> NEW.txn_id THEN
        PERFORM assert_inventory_txn_lot_balance(OLD.txn_id);
      END IF;
    ELSIF TG_OP = 'DELETE' THEN
      IF OLD.txn_id IS NOT NULL THEN
        PERFORM assert_inventory_txn_lot_balance(OLD.txn_id);
      END IF;
    END IF;
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_inventory_txn_lot_balance ON inventory_txns;
CREATE CONSTRAINT TRIGGER trg_inventory_txn_lot_balance
AFTER INSERT OR UPDATE ON inventory_txns
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_check_inventory_txn_lot_balance();

DROP TRIGGER IF EXISTS trg_lot_movement_balance ON lot_movements;
CREATE CONSTRAINT TRIGGER trg_lot_movement_balance
AFTER INSERT OR UPDATE OR DELETE ON lot_movements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_check_inventory_txn_lot_balance();

-- A committed lot balance may never be negative. This protects direct SQL and
-- future code paths in addition to the application FIFO checks.
CREATE OR REPLACE FUNCTION assert_lot_nonnegative(p_lot uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  bal numeric;
BEGIN
  IF p_lot IS NULL THEN
    RETURN;
  END IF;
  SELECT COALESCE(SUM(quantity),0) INTO bal
    FROM lot_movements WHERE lot_id=p_lot;
  IF bal < -0.000001 THEN
    RAISE EXCEPTION 'lot % would have negative physical balance %', p_lot, bal
      USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_check_lot_nonnegative()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP <> 'DELETE' THEN
    PERFORM assert_lot_nonnegative(NEW.lot_id);
  END IF;
  IF TG_OP='DELETE' THEN
    PERFORM assert_lot_nonnegative(OLD.lot_id);
  ELSIF TG_OP='UPDATE' AND OLD.lot_id <> NEW.lot_id THEN
    PERFORM assert_lot_nonnegative(OLD.lot_id);
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_lot_nonnegative ON lot_movements;
CREATE CONSTRAINT TRIGGER trg_lot_nonnegative
AFTER INSERT OR UPDATE OR DELETE ON lot_movements
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_check_lot_nonnegative();

-- --------------------------------------------------------------------------
-- H. Physical stock is lot-backed. Reservations remain item-level.
-- --------------------------------------------------------------------------
CREATE OR REPLACE VIEW v_stock_balance AS
WITH lot_stock AS (
  SELECT l.item_id, COALESCE(SUM(lm.quantity),0) AS on_hand
    FROM lots l
    LEFT JOIN lot_movements lm ON lm.lot_id = l.id
   GROUP BY l.item_id
), reservations AS (
  SELECT item_id,
         COALESCE(SUM(CASE
           WHEN txn_type='RESERVE'   THEN ABS(quantity)
           WHEN txn_type='UNRESERVE' THEN -ABS(quantity)
           ELSE 0 END),0) AS reserved
    FROM inventory_txns
   GROUP BY item_id
)
SELECT i.id AS item_id, i.code, i.name,
       COALESCE(ls.on_hand,0) AS on_hand,
       COALESCE(r.reserved,0) AS reserved
  FROM items i
  LEFT JOIN lot_stock ls ON ls.item_id=i.id
  LEFT JOIN reservations r ON r.item_id=i.id;

-- Diagnostic view. Under the deferred constraints difference must be zero after
-- every committed transaction. Kept for health checks and operations dashboards.
CREATE OR REPLACE VIEW v_inventory_lot_reconciliation AS
WITH item_ledger AS (
  SELECT item_id,
         COALESCE(SUM(CASE WHEN txn_type IN ('RECEIPT','ISSUE','ADJUST') THEN quantity ELSE 0 END),0) AS ledger_on_hand
    FROM inventory_txns GROUP BY item_id
), lot_ledger AS (
  SELECT l.item_id, COALESCE(SUM(lm.quantity),0) AS lot_on_hand
    FROM lots l LEFT JOIN lot_movements lm ON lm.lot_id=l.id
   GROUP BY l.item_id
)
SELECT i.id AS item_id, i.code, i.name,
       COALESCE(il.ledger_on_hand,0) AS ledger_on_hand,
       COALESCE(ll.lot_on_hand,0) AS lot_on_hand,
       COALESCE(il.ledger_on_hand,0) - COALESCE(ll.lot_on_hand,0) AS difference
  FROM items i
  LEFT JOIN item_ledger il ON il.item_id=i.id
  LEFT JOIN lot_ledger ll ON ll.item_id=i.id;

-- Final migration assertion: committed legacy data must already reconcile.
DO $$
DECLARE
  bad record;
BEGIN
  SELECT * INTO bad
    FROM v_inventory_lot_reconciliation
   WHERE abs(difference) > 0.000001
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0018 reconciliation failed for item %: item ledger %, lot ledger %, difference %',
      bad.code, bad.ledger_on_hand, bad.lot_on_hand, bad.difference;
  END IF;
END$$;

COMMIT;
