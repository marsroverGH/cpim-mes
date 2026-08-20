-- ============================================================
-- 0020: Final operation actual -> WO finished-goods receipt guard
-- ============================================================
-- Invariants after this migration:
--   1) A WO with physical completion must have a final Shop Floor operation.
--   2) SUM(work_order_completions.quantity) = work_orders.completed_qty.
--   3) WO completed/received quantity <= final operation completed_qty.
--   4) A NEW work_order_completions row must reference the exact RECEIPT ledger
--      transaction and PRODUCED lot allocation created for that completion.
--   5) These rules are DEFERRABLE so one application transaction may post the
--      receipt, completion history and WO cumulative quantity atomically.

BEGIN;

ALTER TABLE work_order_completions
  ADD COLUMN IF NOT EXISTS receipt_txn_id uuid;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname='work_order_completions_receipt_txn_fkey'
       AND conrelid='work_order_completions'::regclass
  ) THEN
    ALTER TABLE work_order_completions
      ADD CONSTRAINT work_order_completions_receipt_txn_fkey
      FOREIGN KEY (receipt_txn_id) REFERENCES inventory_txns(id) ON DELETE RESTRICT;
  END IF;
END$$;

CREATE UNIQUE INDEX IF NOT EXISTS wo_completions_receipt_txn_uq
  ON work_order_completions(receipt_txn_id)
  WHERE receipt_txn_id IS NOT NULL;

-- Best-effort bind of completions produced by the post-0015 workflow. Legacy
-- completions that predate the COMP:<uuid> reference convention remain NULL and
-- are grandfathered; all NEW completion rows are required to be bound below.
UPDATE work_order_completions c
   SET receipt_txn_id = (
     SELECT t.id
       FROM work_orders w
       JOIN inventory_txns t
         ON t.item_id=w.item_id
        AND t.txn_type='RECEIPT'
        AND t.ref_doc=('WO:' || w.order_no || ':COMP:' || c.id::text)
      WHERE w.id=c.work_order_id
      ORDER BY t.occurred_at, t.id
      LIMIT 1
   )
 WHERE c.receipt_txn_id IS NULL
   AND EXISTS (
     SELECT 1
       FROM work_orders w
       JOIN inventory_txns t
         ON t.item_id=w.item_id
        AND t.txn_type='RECEIPT'
        AND t.ref_doc=('WO:' || w.order_no || ':COMP:' || c.id::text)
      WHERE w.id=c.work_order_id
   );

-- Every newly inserted completion must be backed by the exact finished-goods
-- receipt transaction and its produced-lot movement. Completion rows are
-- immutable after insert; corrections must use an explicit reversal workflow.
CREATE OR REPLACE FUNCTION enforce_wo_completion_receipt_link()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  wo_order_no text;
  wo_item_id uuid;
  v_txn_item_id uuid;
  v_txn_qty numeric;
  v_txn_type text;
  v_txn_ref text;
  lot_qty numeric;
  expected_ref text;
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'work_order_completions rows are immutable; use a reversal workflow'
      USING ERRCODE='23514';
  END IF;

  IF TG_OP='UPDATE' THEN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.work_order_id IS DISTINCT FROM OLD.work_order_id
       OR NEW.quantity IS DISTINCT FROM OLD.quantity
       OR NEW.produced_lot_id IS DISTINCT FROM OLD.produced_lot_id
       OR NEW.receipt_txn_id IS DISTINCT FROM OLD.receipt_txn_id THEN
      RAISE EXCEPTION 'work_order_completions rows are immutable; use a reversal workflow'
        USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.receipt_txn_id IS NULL THEN
    RAISE EXCEPTION 'new WO completion % must reference its finished-goods RECEIPT transaction', NEW.id
      USING ERRCODE='23514';
  END IF;

  SELECT order_no, item_id INTO wo_order_no, wo_item_id
    FROM work_orders WHERE id=NEW.work_order_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'work order % does not exist', NEW.work_order_id
      USING ERRCODE='23503';
  END IF;

  SELECT item_id, quantity, txn_type, ref_doc
    INTO v_txn_item_id, v_txn_qty, v_txn_type, v_txn_ref
    FROM inventory_txns WHERE id=NEW.receipt_txn_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'receipt transaction % does not exist', NEW.receipt_txn_id
      USING ERRCODE='23503';
  END IF;

  expected_ref := 'WO:' || wo_order_no || ':COMP:' || NEW.id::text;
  IF v_txn_type <> 'RECEIPT'
     OR v_txn_item_id <> wo_item_id
     OR abs(v_txn_qty - NEW.quantity) > 0.000001
     OR v_txn_ref <> expected_ref THEN
    RAISE EXCEPTION
      'completion % receipt transaction mismatch: expected item %, qty %, ref %, got type %, item %, qty %, ref %',
      NEW.id, wo_item_id, NEW.quantity, expected_ref,
      v_txn_type, v_txn_item_id, v_txn_qty, v_txn_ref
      USING ERRCODE='23514';
  END IF;

  SELECT COALESCE(SUM(quantity),0) INTO lot_qty
    FROM lot_movements
   WHERE txn_id=NEW.receipt_txn_id
     AND lot_id=NEW.produced_lot_id
     AND movement_type='PRODUCED';
  IF abs(lot_qty - NEW.quantity) > 0.000001 THEN
    RAISE EXCEPTION
      'completion % produced lot allocation % does not equal completion quantity % (allocated %)',
      NEW.id, NEW.produced_lot_id, NEW.quantity, lot_qty
      USING ERRCODE='23514';
  END IF;

  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_wo_completion_receipt_link ON work_order_completions;
CREATE TRIGGER trg_wo_completion_receipt_link
BEFORE INSERT OR UPDATE OR DELETE ON work_order_completions
FOR EACH ROW EXECUTE FUNCTION enforce_wo_completion_receipt_link();

-- A RECEIPT carrying the workflow's WO:<orderNo>:COMP:<completionId> reference
-- must itself be linked from exactly one completion row by COMMIT. This closes
-- the inverse orphan path: direct SQL cannot add WO finished-goods stock while
-- omitting work_order_completions/work_orders.completed_qty.
CREATE OR REPLACE FUNCTION assert_wo_comp_receipt_has_completion(p_txn uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  v_type text;
  v_ref text;
  linked_count integer;
BEGIN
  SELECT txn_type, ref_doc INTO v_type, v_ref
    FROM inventory_txns WHERE id=p_txn;
  IF NOT FOUND OR v_type <> 'RECEIPT' OR v_ref NOT LIKE 'WO:%:COMP:%' THEN
    RETURN;
  END IF;

  SELECT COUNT(*) INTO linked_count
    FROM work_order_completions
   WHERE receipt_txn_id=p_txn;
  IF linked_count <> 1 THEN
    RAISE EXCEPTION
      'WO completion receipt transaction % must be linked by exactly one work_order_completions row (found %)',
      p_txn, linked_count USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_wo_comp_receipt_has_completion()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP <> 'DELETE' THEN
    PERFORM assert_wo_comp_receipt_has_completion(NEW.id);
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_wo_comp_receipt_has_completion ON inventory_txns;
CREATE CONSTRAINT TRIGGER trg_wo_comp_receipt_has_completion
AFTER INSERT OR UPDATE ON inventory_txns
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_wo_comp_receipt_has_completion();

-- Once linked to a completion, the receipt header and its lot allocations are
-- immutable. A future correction must post an explicit reversal rather than
-- rewriting production history.
CREATE OR REPLACE FUNCTION prevent_bound_wo_receipt_mutation()
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
    SELECT 1 FROM work_order_completions WHERE receipt_txn_id=txn_to_check
  ) THEN
    RAISE EXCEPTION 'WO completion receipt % is immutable; use a reversal workflow', txn_to_check
      USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_no_mutate_bound_wo_receipt_txn ON inventory_txns;
CREATE TRIGGER trg_no_mutate_bound_wo_receipt_txn
BEFORE UPDATE OR DELETE ON inventory_txns
FOR EACH ROW EXECUTE FUNCTION prevent_bound_wo_receipt_mutation();

DROP TRIGGER IF EXISTS trg_no_mutate_bound_wo_receipt_lot_mv ON lot_movements;
CREATE TRIGGER trg_no_mutate_bound_wo_receipt_lot_mv
BEFORE UPDATE OR DELETE ON lot_movements
FOR EACH ROW EXECUTE FUNCTION prevent_bound_wo_receipt_mutation();

-- Core cumulative invariant. The final operation is the operation with the
-- highest seq_no on the WO. A partial final-operation report can authorize the
-- same amount of cumulative finished-goods receipt even while the operation
-- itself remains IN_PROGRESS.
CREATE OR REPLACE FUNCTION assert_wo_within_final_operation(p_wo uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  planned_qty numeric;
  wo_completed numeric;
  final_completed numeric;
  final_status text;
  completion_sum numeric;
  final_seq integer;
BEGIN
  IF p_wo IS NULL THEN
    RETURN;
  END IF;

  SELECT quantity, completed_qty
    INTO planned_qty, wo_completed
    FROM work_orders WHERE id=p_wo;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  SELECT seq_no, completed_qty, status
    INTO final_seq, final_completed, final_status
    FROM wo_operations
   WHERE wo_id=p_wo
   ORDER BY seq_no DESC
   LIMIT 1;

  SELECT COALESCE(SUM(quantity),0)
    INTO completion_sum
    FROM work_order_completions
   WHERE work_order_id=p_wo;

  IF final_seq IS NULL THEN
    IF wo_completed > 0.000001 OR completion_sum > 0.000001 THEN
      RAISE EXCEPTION
        'WO % has completed/received quantity but no final Shop Floor operation', p_wo
        USING ERRCODE='23514';
    END IF;
    RETURN;
  END IF;

  IF final_completed > 0.000001 AND final_status='PENDING' THEN
    RAISE EXCEPTION
      'WO % final operation has completed quantity % while status is PENDING',
      p_wo, final_completed
      USING ERRCODE='23514';
  END IF;

  IF final_completed < -0.000001 OR final_completed > planned_qty + 0.000001 THEN
    RAISE EXCEPTION
      'WO % final operation quantity % is outside planned quantity 0..%',
      p_wo, final_completed, planned_qty
      USING ERRCODE='23514';
  END IF;

  IF abs(wo_completed - completion_sum) > 0.000001 THEN
    RAISE EXCEPTION
      'WO % completed_qty % does not equal completion history sum %',
      p_wo, wo_completed, completion_sum
      USING ERRCODE='23514';
  END IF;

  IF wo_completed > final_completed + 0.000001 THEN
    RAISE EXCEPTION
      'WO % finished-goods received % exceeds final operation actual % (seq %)',
      p_wo, wo_completed, final_completed, final_seq
      USING ERRCODE='23514';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION trg_assert_wo_within_final_operation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  wo_id_to_check uuid;
BEGIN
  IF TG_TABLE_NAME='work_orders' THEN
    -- Avoid breaking untouched legacy rows: only completed_qty changes require
    -- a revalidation. New rows start at zero and are harmless without routing.
    IF TG_OP='UPDATE' AND NEW.completed_qty IS NOT DISTINCT FROM OLD.completed_qty THEN
      RETURN NULL;
    END IF;
    wo_id_to_check := NEW.id;
  ELSIF TG_TABLE_NAME='work_order_completions' THEN
    IF TG_OP='DELETE' THEN
      wo_id_to_check := OLD.work_order_id;
    ELSE
      wo_id_to_check := NEW.work_order_id;
    END IF;
  ELSE
    IF TG_OP='DELETE' THEN
      wo_id_to_check := OLD.wo_id;
    ELSE
      wo_id_to_check := NEW.wo_id;
    END IF;
  END IF;

  PERFORM assert_wo_within_final_operation(wo_id_to_check);
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_wo_final_op_guard_work_orders ON work_orders;
CREATE CONSTRAINT TRIGGER trg_wo_final_op_guard_work_orders
AFTER INSERT OR UPDATE ON work_orders
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_wo_within_final_operation();

DROP TRIGGER IF EXISTS trg_wo_final_op_guard_completions ON work_order_completions;
CREATE CONSTRAINT TRIGGER trg_wo_final_op_guard_completions
AFTER INSERT OR UPDATE OR DELETE ON work_order_completions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_wo_within_final_operation();

DROP TRIGGER IF EXISTS trg_wo_final_op_guard_operations ON wo_operations;
CREATE CONSTRAINT TRIGGER trg_wo_final_op_guard_operations
AFTER INSERT OR UPDATE OR DELETE ON wo_operations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION trg_assert_wo_within_final_operation();

-- Diagnostic view used after migration and by operations/support teams.
CREATE OR REPLACE VIEW v_wo_final_operation_reconciliation AS
WITH final_ops AS (
  SELECT DISTINCT ON (wo_id)
         wo_id, id AS final_operation_id, seq_no AS final_seq_no,
         completed_qty AS final_operation_completed_qty,
         status AS final_operation_status
    FROM wo_operations
   ORDER BY wo_id, seq_no DESC
), completions AS (
  SELECT work_order_id, COALESCE(SUM(quantity),0) AS completion_history_qty,
         COUNT(*) FILTER (WHERE receipt_txn_id IS NULL) AS legacy_unbound_completion_count
    FROM work_order_completions
   GROUP BY work_order_id
)
SELECT w.id AS work_order_id, w.order_no, w.quantity AS planned_qty,
       w.completed_qty AS wo_received_qty,
       COALESCE(c.completion_history_qty,0) AS completion_history_qty,
       f.final_operation_id, f.final_seq_no,
       COALESCE(f.final_operation_completed_qty,0) AS final_operation_completed_qty,
       f.final_operation_status,
       GREATEST(COALESCE(f.final_operation_completed_qty,0)-w.completed_qty,0) AS receipt_available_qty,
       COALESCE(c.legacy_unbound_completion_count,0) AS legacy_unbound_completion_count,
       CASE
         WHEN w.completed_qty > 0 AND f.final_operation_id IS NULL THEN false
         WHEN abs(w.completed_qty-COALESCE(c.completion_history_qty,0)) > 0.000001 THEN false
         WHEN COALESCE(f.final_operation_completed_qty,0) > w.quantity+0.000001 THEN false
         WHEN COALESCE(f.final_operation_completed_qty,0) > 0.000001 AND f.final_operation_status='PENDING' THEN false
         WHEN w.completed_qty > COALESCE(f.final_operation_completed_qty,0)+0.000001 THEN false
         ELSE true
       END AS is_consistent
  FROM work_orders w
  LEFT JOIN final_ops f ON f.wo_id=w.id
  LEFT JOIN completions c ON c.work_order_id=w.id;

-- Refuse to silently certify an already-inconsistent database. Operators must
-- reconcile legacy WOs before retrying this migration.
DO $$
DECLARE
  bad_count integer;
  orphan_receipts integer;
BEGIN
  SELECT COUNT(*) INTO bad_count
    FROM v_wo_final_operation_reconciliation
   WHERE NOT is_consistent;
  IF bad_count > 0 THEN
    RAISE EXCEPTION
      '0020 final-operation guard found % inconsistent legacy work order(s); query v_wo_final_operation_reconciliation and reconcile before retrying',
      bad_count USING ERRCODE='23514';
  END IF;

  SELECT COUNT(*) INTO orphan_receipts
    FROM inventory_txns t
   WHERE t.txn_type='RECEIPT'
     AND t.ref_doc LIKE 'WO:%:COMP:%'
     AND NOT EXISTS (
       SELECT 1 FROM work_order_completions c WHERE c.receipt_txn_id=t.id
     );
  IF orphan_receipts > 0 THEN
    RAISE EXCEPTION
      '0020 final-operation guard found % orphan WO completion receipt(s); reconcile before retrying',
      orphan_receipts USING ERRCODE='23514';
  END IF;
END$$;

COMMIT;
