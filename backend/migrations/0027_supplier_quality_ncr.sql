-- ============================================================
-- 0027: Supplier Quality / NCR
-- ============================================================
-- Adds supplier qualification profiles, immutable NCR/disposition history,
-- incoming inspection quarantine, blocked-supplier enforcement, and supplier
-- quality scorecards. Supplier-derived FAIL inspections auto-open an NCR.

-- Supplier qualification profile. Existing suppliers are backfilled as APPROVED
-- with inspection_required=false to preserve legacy behavior.
CREATE TABLE IF NOT EXISTS supplier_quality_profiles (
  supplier_name        text PRIMARY KEY,
  status               text NOT NULL DEFAULT 'APPROVED'
                       CHECK (status IN ('APPROVED','CONDITIONAL','BLOCKED')),
  inspection_required  boolean NOT NULL DEFAULT false,
  target_ppm           numeric NOT NULL DEFAULT 0 CHECK (target_ppm >= 0),
  notes                text NOT NULL DEFAULT '',
  updated_by_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  updated_by           text NOT NULL DEFAULT '',
  updated_at           timestamptz NOT NULL DEFAULT now()
);

INSERT INTO supplier_quality_profiles(supplier_name, status, inspection_required, notes)
SELECT supplier, 'APPROVED', false, 'LEGACY_RECONSTRUCTED'
  FROM (
    SELECT DISTINCT btrim(supplier) AS supplier FROM purchase_orders
    UNION
    SELECT DISTINCT btrim(supplier) AS supplier FROM lots
  ) s
 WHERE supplier <> ''
ON CONFLICT (supplier_name) DO NOTHING;

-- Extend lot quality history so non-inspection supplier quality events can be
-- included in the same immutable quality timeline.
ALTER TABLE quality_status_history ALTER COLUMN inspection_id DROP NOT NULL;
ALTER TABLE quality_status_history DROP CONSTRAINT IF EXISTS quality_status_history_inspection_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS ux_quality_status_history_inspection
  ON quality_status_history(inspection_id) WHERE inspection_id IS NOT NULL;
ALTER TABLE quality_status_history
  ADD COLUMN IF NOT EXISTS source_ref text NOT NULL DEFAULT '';

-- Supplier NCR header. One FAIL inspection can open at most one NCR.
CREATE TABLE IF NOT EXISTS supplier_ncrs (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ncr_no               text NOT NULL UNIQUE,
  supplier             text NOT NULL,
  purchase_order_id    uuid REFERENCES purchase_orders(id) ON DELETE RESTRICT,
  purchase_receipt_id  uuid REFERENCES purchase_receipts(id) ON DELETE RESTRICT,
  item_id              uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  lot_id               uuid NOT NULL REFERENCES lots(id) ON DELETE RESTRICT,
  inspection_id        uuid REFERENCES quality_inspections(id) ON DELETE RESTRICT,
  affected_qty         numeric NOT NULL DEFAULT 0 CHECK (affected_qty >= 0),
  severity             text NOT NULL DEFAULT 'MAJOR'
                       CHECK (severity IN ('MINOR','MAJOR','CRITICAL')),
  description          text NOT NULL DEFAULT '',
  status               text NOT NULL DEFAULT 'OPEN'
                       CHECK (status IN ('OPEN','IN_REWORK','CLOSED','CANCELLED')),
  created_by_user_id   uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_by           text NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  closed_by_user_id    uuid REFERENCES users(id) ON DELETE RESTRICT,
  closed_by            text NOT NULL DEFAULT '',
  closed_at            timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_supplier_ncr_inspection
  ON supplier_ncrs(inspection_id) WHERE inspection_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS supplier_ncr_supplier_idx ON supplier_ncrs(supplier, created_at DESC);
CREATE INDEX IF NOT EXISTS supplier_ncr_lot_idx ON supplier_ncrs(lot_id, status);
CREATE INDEX IF NOT EXISTS supplier_ncr_status_idx ON supplier_ncrs(status, created_at DESC);

-- One disposition decision per NCR. REWORK is non-final and requires a later
-- PASS inspection before close; the other dispositions close the NCR.
CREATE TABLE IF NOT EXISTS supplier_ncr_dispositions (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ncr_id              uuid NOT NULL UNIQUE REFERENCES supplier_ncrs(id) ON DELETE RESTRICT,
  disposition         text NOT NULL
                      CHECK (disposition IN ('RETURN_TO_SUPPLIER','SCRAP','REWORK','USE_AS_IS')),
  quantity            numeric NOT NULL CHECK (quantity > 0),
  notes               text NOT NULL DEFAULT '',
  inventory_txn_id    uuid REFERENCES inventory_txns(id) ON DELETE RESTRICT,
  decided_by_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  decided_by          text NOT NULL,
  decided_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS supplier_ncr_history (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ncr_id              uuid NOT NULL REFERENCES supplier_ncrs(id) ON DELETE RESTRICT,
  from_status         text,
  to_status           text NOT NULL,
  event_type          text NOT NULL,
  actor_user_id       uuid REFERENCES users(id) ON DELETE RESTRICT,
  actor               text NOT NULL DEFAULT '',
  occurred_at         timestamptz NOT NULL DEFAULT now(),
  notes               text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS supplier_ncr_history_idx
  ON supplier_ncr_history(ncr_id, occurred_at, id);

-- Expand physical movement types used by NCR final dispositions.
ALTER TABLE lot_movements DROP CONSTRAINT IF EXISTS lot_movements_movement_type_check;
ALTER TABLE lot_movements ADD CONSTRAINT lot_movements_movement_type_check
  CHECK (movement_type IN (
    'RECEIPT','ISSUE','ADJUST','CONSUMED','PRODUCED',
    'RETURN_TO_SUPPLIER','SCRAP'
  ));


-- 0018 originally allowed only movement_type='ADJUST' under an ADJUST header.
-- NCR RETURN/SCRAP are intentionally negative physical adjustments with distinct
-- traceability movement types. Override both the immediate and deferred ledger
-- validators so only those two negative NCR movement types are additionally legal.
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
    RAISE EXCEPTION 'lot movement % must reference inventory_txns', NEW.id USING ERRCODE='23514';
  END IF;
  SELECT item_id INTO lot_item FROM lots WHERE id=NEW.lot_id;
  SELECT item_id, txn_type, ref_doc, quantity INTO txn_item, txn_kind, txn_ref, txn_qty
    FROM inventory_txns WHERE id=NEW.txn_id;
  IF txn_item IS NULL THEN
    RAISE EXCEPTION 'inventory transaction % does not exist', NEW.txn_id USING ERRCODE='23503';
  END IF;
  IF lot_item <> txn_item THEN
    RAISE EXCEPTION 'lot item % does not match inventory transaction item %', lot_item, txn_item USING ERRCODE='23514';
  END IF;
  IF txn_kind IN ('RESERVE','UNRESERVE') THEN
    RAISE EXCEPTION '% transaction % cannot have a physical lot movement', txn_kind, NEW.txn_id USING ERRCODE='23514';
  END IF;
  IF NEW.quantity = 0 THEN
    RAISE EXCEPTION 'lot movement quantity must be non-zero' USING ERRCODE='23514';
  END IF;

  IF txn_kind='RECEIPT' AND
     (NEW.quantity<=0 OR NEW.movement_type NOT IN ('RECEIPT','PRODUCED')) THEN
    RAISE EXCEPTION 'RECEIPT transaction requires positive RECEIPT/PRODUCED lot movement' USING ERRCODE='23514';
  ELSIF txn_kind='ISSUE' AND
        (NEW.quantity>=0 OR NEW.movement_type NOT IN ('ISSUE','CONSUMED')) THEN
    RAISE EXCEPTION 'ISSUE transaction requires negative ISSUE/CONSUMED lot movement' USING ERRCODE='23514';
  ELSIF txn_kind='ADJUST' AND NOT (
        (NEW.movement_type='ADJUST' AND NEW.quantity*txn_qty>0)
        OR (NEW.movement_type IN ('RETURN_TO_SUPPLIER','SCRAP') AND NEW.quantity<0 AND txn_qty<0)
      ) THEN
    RAISE EXCEPTION 'ADJUST transaction requires same-sign ADJUST or negative NCR RETURN/SCRAP lot movement' USING ERRCODE='23514';
  END IF;
  IF COALESCE(NEW.ref_doc,'') <> COALESCE(txn_ref,'') THEN
    RAISE EXCEPTION 'lot movement ref_doc must equal inventory transaction ref_doc' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION assert_inventory_txn_lot_balance(p_txn uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  t record;
  allocated numeric;
  invalid_allocations integer;
BEGIN
  SELECT id,item_id,quantity,txn_type,ref_doc INTO t FROM inventory_txns WHERE id=p_txn;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT COALESCE(SUM(quantity),0) INTO allocated FROM lot_movements WHERE txn_id=p_txn;
  SELECT COUNT(*) INTO invalid_allocations
    FROM lot_movements lm JOIN lots l ON l.id=lm.lot_id
   WHERE lm.txn_id=p_txn AND (
       l.item_id<>t.item_id
       OR COALESCE(lm.ref_doc,'')<>COALESCE(t.ref_doc,'')
       OR (t.txn_type='RECEIPT' AND (lm.quantity<=0 OR lm.movement_type NOT IN ('RECEIPT','PRODUCED')))
       OR (t.txn_type='ISSUE' AND (lm.quantity>=0 OR lm.movement_type NOT IN ('ISSUE','CONSUMED')))
       OR (t.txn_type='ADJUST' AND NOT (
            (lm.movement_type='ADJUST' AND lm.quantity*t.quantity>0)
            OR (lm.movement_type IN ('RETURN_TO_SUPPLIER','SCRAP') AND lm.quantity<0 AND t.quantity<0)
          ))
       OR t.txn_type IN ('RESERVE','UNRESERVE')
     );
  IF invalid_allocations>0 THEN
    RAISE EXCEPTION 'inventory transaction % has % invalid lot allocation(s)',p_txn,invalid_allocations USING ERRCODE='23514';
  END IF;
  IF t.txn_type IN ('RECEIPT','ISSUE','ADJUST') THEN
    IF abs(t.quantity-allocated)>0.000001 THEN
      RAISE EXCEPTION 'inventory transaction % quantity % is not fully lot-allocated (allocated %)',p_txn,t.quantity,allocated USING ERRCODE='23514';
    END IF;
  ELSE
    IF abs(allocated)>0.000001 THEN
      RAISE EXCEPTION 'logical inventory transaction % (%) must not have lot allocation',p_txn,t.txn_type USING ERRCODE='23514';
    END IF;
  END IF;
END$$;

-- Profile audit/authorization. Planner/admin can manage supplier profiles.
CREATE OR REPLACE FUNCTION supplier_quality_profile_guard()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_user users%ROWTYPE;
BEGIN
  NEW.supplier_name := btrim(NEW.supplier_name);
  IF NEW.supplier_name = '' THEN
    RAISE EXCEPTION 'supplier_name is required';
  END IF;
  IF NEW.updated_by_user_id IS NULL THEN
    RAISE EXCEPTION 'supplier profile update requires updated_by_user_id';
  END IF;
  SELECT * INTO v_user FROM users WHERE id=NEW.updated_by_user_id;
  IF NOT FOUND OR NOT v_user.is_active OR v_user.role NOT IN ('planner','admin') THEN
    RAISE EXCEPTION 'supplier profile updater must be an active planner/admin';
  END IF;
  NEW.updated_by := v_user.username;
  NEW.updated_at := transaction_timestamp();
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_quality_profile_guard_trg ON supplier_quality_profiles;
CREATE TRIGGER supplier_quality_profile_guard_trg
BEFORE INSERT OR UPDATE ON supplier_quality_profiles
FOR EACH ROW EXECUTE FUNCTION supplier_quality_profile_guard();

-- Prevent creation/update of POs for suppliers explicitly BLOCKED by Supplier Quality.
CREATE OR REPLACE FUNCTION guard_blocked_supplier_po()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_status text;
BEGIN
  SELECT status INTO v_status FROM supplier_quality_profiles WHERE supplier_name=btrim(NEW.supplier);
  IF v_status = 'BLOCKED' THEN
    RAISE EXCEPTION 'supplier % is BLOCKED by Supplier Quality', NEW.supplier;
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS guard_blocked_supplier_po_trg ON purchase_orders;
CREATE TRIGGER guard_blocked_supplier_po_trg
BEFORE INSERT OR UPDATE OF supplier ON purchase_orders
FOR EACH ROW EXECUTE FUNCTION guard_blocked_supplier_po();

-- Before a purchase receipt business event commits, reject blocked suppliers.
CREATE OR REPLACE FUNCTION guard_blocked_supplier_receipt()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_supplier text;
  v_status text;
BEGIN
  SELECT supplier INTO v_supplier FROM purchase_orders WHERE id=NEW.purchase_order_id;
  SELECT status INTO v_status FROM supplier_quality_profiles WHERE supplier_name=btrim(v_supplier);
  IF v_status = 'BLOCKED' THEN
    RAISE EXCEPTION 'supplier % is BLOCKED; receipt is not allowed', v_supplier;
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS guard_blocked_supplier_receipt_trg ON purchase_receipts;
CREATE TRIGGER guard_blocked_supplier_receipt_trg
BEFORE INSERT ON purchase_receipts
FOR EACH ROW EXECUTE FUNCTION guard_blocked_supplier_receipt();

-- Quarantine incoming lots when supplier inspection is required. This update is
-- nested inside a purchase_receipts trigger and therefore passes the existing
-- direct lot-quality mutation guard. A unified quality-history event is appended.
CREATE OR REPLACE FUNCTION supplier_receipt_quality_hold()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_supplier text;
  v_required boolean;
  v_old text;
BEGIN
  SELECT po.supplier, COALESCE(sqp.inspection_required,false)
    INTO v_supplier, v_required
    FROM purchase_orders po
    LEFT JOIN supplier_quality_profiles sqp ON sqp.supplier_name=btrim(po.supplier)
   WHERE po.id=NEW.purchase_order_id;

  IF v_required THEN
    SELECT quality_status INTO v_old FROM lots WHERE id=NEW.lot_id FOR UPDATE;
    IF v_old = 'OK' THEN
      UPDATE lots SET quality_status='HOLD' WHERE id=NEW.lot_id;
      INSERT INTO quality_status_history(
        lot_id, inspection_id, from_status, to_status,
        changed_by_user_id, changed_by, changed_at, source, source_ref, notes
      ) VALUES (
        NEW.lot_id, NULL, v_old, 'HOLD',
        NEW.received_by_user_id, NEW.received_by_username, NEW.received_at,
        'SUPPLIER_RECEIPT_HOLD', NEW.id::text,
        'Incoming inspection required for supplier ' || v_supplier
      );
    END IF;
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_receipt_quality_hold_trg ON purchase_receipts;
CREATE TRIGGER supplier_receipt_quality_hold_trg
AFTER INSERT ON purchase_receipts
FOR EACH ROW EXECUTE FUNCTION supplier_receipt_quality_hold();

-- Supplier NCR evidence is append-oriented. Header may change only status/close fields.
CREATE OR REPLACE FUNCTION supplier_ncr_before_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_user users%ROWTYPE; v_supplier text; v_item uuid; v_status text;
BEGIN
  SELECT * INTO v_user FROM users WHERE id=NEW.created_by_user_id;
  IF NOT FOUND OR NOT v_user.is_active OR v_user.role NOT IN ('operator','planner','admin') THEN
    RAISE EXCEPTION 'NCR creator must be active operator/planner/admin';
  END IF;
  SELECT supplier, item_id, quality_status INTO v_supplier, v_item, v_status
    FROM lots WHERE id=NEW.lot_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'NCR lot % does not exist', NEW.lot_id; END IF;
  IF btrim(v_supplier) = '' THEN RAISE EXCEPTION 'Supplier NCR requires a supplier-derived lot'; END IF;
  IF NEW.supplier IS DISTINCT FROM v_supplier OR NEW.item_id IS DISTINCT FROM v_item THEN
    RAISE EXCEPTION 'NCR supplier/item must match lot';
  END IF;
  NEW.created_by := v_user.username;
  NEW.created_at := transaction_timestamp();
  NEW.status := 'OPEN';
  NEW.closed_by_user_id := NULL; NEW.closed_by := ''; NEW.closed_at := NULL;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_before_insert_trg ON supplier_ncrs;
CREATE TRIGGER supplier_ncr_before_insert_trg
BEFORE INSERT ON supplier_ncrs
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_before_insert();

CREATE OR REPLACE FUNCTION supplier_ncr_after_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_status text;
BEGIN
  SELECT quality_status INTO v_status FROM lots WHERE id=NEW.lot_id FOR UPDATE;
  IF v_status = 'OK' THEN
    UPDATE lots SET quality_status='HOLD' WHERE id=NEW.lot_id;
    INSERT INTO quality_status_history(
      lot_id, inspection_id, from_status, to_status,
      changed_by_user_id, changed_by, changed_at, source, source_ref, notes
    ) VALUES (
      NEW.lot_id, NULL, 'OK', 'HOLD', NEW.created_by_user_id, NEW.created_by,
      NEW.created_at, 'NCR_OPEN', NEW.id::text, 'NCR ' || NEW.ncr_no || ' opened'
    );
  END IF;
  INSERT INTO supplier_ncr_history(
    ncr_id, from_status, to_status, event_type, actor_user_id, actor, occurred_at, notes
  ) VALUES (NEW.id, NULL, 'OPEN', 'CREATED', NEW.created_by_user_id, NEW.created_by, NEW.created_at, NEW.description);
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_after_insert_trg ON supplier_ncrs;
CREATE TRIGGER supplier_ncr_after_insert_trg
AFTER INSERT ON supplier_ncrs
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_after_insert();

CREATE OR REPLACE FUNCTION supplier_ncr_before_update()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_user users%ROWTYPE; v_pass_count integer;
BEGIN
  -- Immutable business identity/evidence fields.
  IF NEW.ncr_no IS DISTINCT FROM OLD.ncr_no OR NEW.supplier IS DISTINCT FROM OLD.supplier
     OR NEW.purchase_order_id IS DISTINCT FROM OLD.purchase_order_id
     OR NEW.purchase_receipt_id IS DISTINCT FROM OLD.purchase_receipt_id
     OR NEW.item_id IS DISTINCT FROM OLD.item_id OR NEW.lot_id IS DISTINCT FROM OLD.lot_id
     OR NEW.inspection_id IS DISTINCT FROM OLD.inspection_id
     OR NEW.affected_qty IS DISTINCT FROM OLD.affected_qty
     OR NEW.severity IS DISTINCT FROM OLD.severity OR NEW.description IS DISTINCT FROM OLD.description
     OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id
     OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'NCR evidence fields are immutable';
  END IF;
  IF NEW.status = OLD.status THEN RETURN NEW; END IF;
  IF OLD.status='OPEN' AND NEW.status NOT IN ('IN_REWORK','CLOSED','CANCELLED') THEN
    RAISE EXCEPTION 'invalid NCR transition % -> %', OLD.status, NEW.status;
  ELSIF OLD.status='IN_REWORK' AND NEW.status NOT IN ('CLOSED','CANCELLED') THEN
    RAISE EXCEPTION 'invalid NCR transition % -> %', OLD.status, NEW.status;
  ELSIF OLD.status IN ('CLOSED','CANCELLED') THEN
    RAISE EXCEPTION 'terminal NCR status % cannot transition', OLD.status;
  END IF;
  IF NEW.status='CLOSED' AND OLD.status='IN_REWORK' THEN
    SELECT count(*) INTO v_pass_count
      FROM quality_inspections qi
      JOIN supplier_ncr_dispositions d ON d.ncr_id=OLD.id
     WHERE qi.lot_id=OLD.lot_id AND qi.result='PASS' AND qi.inspected_at > d.decided_at;
    IF v_pass_count=0 THEN
      RAISE EXCEPTION 'REWORK NCR can close only after a later PASS inspection';
    END IF;
  END IF;
  IF NEW.status IN ('CLOSED','CANCELLED') THEN
    IF NEW.closed_by_user_id IS NULL THEN RAISE EXCEPTION 'closing NCR requires actor'; END IF;
    SELECT * INTO v_user FROM users WHERE id=NEW.closed_by_user_id;
    IF NOT FOUND OR NOT v_user.is_active OR v_user.role NOT IN ('planner','admin') THEN
      RAISE EXCEPTION 'NCR closer must be active planner/admin';
    END IF;
    NEW.closed_by := v_user.username;
    NEW.closed_at := transaction_timestamp();
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_before_update_trg ON supplier_ncrs;
CREATE TRIGGER supplier_ncr_before_update_trg
BEFORE UPDATE ON supplier_ncrs
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_before_update();

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
      transaction_timestamp(), ''
    );
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_after_update_trg ON supplier_ncrs;
CREATE TRIGGER supplier_ncr_after_update_trg
AFTER UPDATE ON supplier_ncrs
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_after_update();

-- Validate dispositions and verify physical stock links for RETURN/SCRAP.
CREATE OR REPLACE FUNCTION supplier_ncr_disposition_before_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_ncr supplier_ncrs%ROWTYPE;
  v_user users%ROWTYPE;
  v_txn inventory_txns%ROWTYPE;
  v_alloc numeric;
BEGIN
  SELECT * INTO v_ncr FROM supplier_ncrs WHERE id=NEW.ncr_id FOR UPDATE;
  IF NOT FOUND OR v_ncr.status <> 'OPEN' THEN RAISE EXCEPTION 'NCR must be OPEN for disposition'; END IF;
  SELECT * INTO v_user FROM users WHERE id=NEW.decided_by_user_id;
  IF NOT FOUND OR NOT v_user.is_active OR v_user.role NOT IN ('planner','admin') THEN
    RAISE EXCEPTION 'NCR disposition requires active planner/admin';
  END IF;
  IF NEW.disposition='USE_AS_IS' AND v_user.role <> 'admin' THEN
    RAISE EXCEPTION 'USE_AS_IS requires admin approval';
  END IF;
  IF NEW.disposition='USE_AS_IS' AND EXISTS (
    SELECT 1 FROM supplier_ncrs x
     WHERE x.lot_id=v_ncr.lot_id AND x.id<>v_ncr.id AND x.status IN ('OPEN','IN_REWORK')
  ) THEN
    RAISE EXCEPTION 'USE_AS_IS cannot release lot while another active NCR exists';
  END IF;
  IF v_ncr.affected_qty > 0 AND NEW.quantity > v_ncr.affected_qty + 0.000001 THEN
    RAISE EXCEPTION 'disposition quantity % exceeds affected quantity %', NEW.quantity, v_ncr.affected_qty;
  END IF;
  IF NEW.disposition IN ('RETURN_TO_SUPPLIER','SCRAP') THEN
    IF NEW.inventory_txn_id IS NULL THEN RAISE EXCEPTION '% requires inventory transaction', NEW.disposition; END IF;
    SELECT * INTO v_txn FROM inventory_txns WHERE id=NEW.inventory_txn_id;
    IF NOT FOUND OR v_txn.item_id<>v_ncr.item_id OR v_txn.txn_type<>'ADJUST'
       OR abs(v_txn.quantity + NEW.quantity) > 0.000001 THEN
      RAISE EXCEPTION 'NCR physical disposition inventory transaction mismatch';
    END IF;
    SELECT COALESCE(sum(-lm.quantity),0) INTO v_alloc FROM lot_movements lm
     WHERE lm.txn_id=NEW.inventory_txn_id AND lm.lot_id=v_ncr.lot_id
       AND lm.movement_type=NEW.disposition;
    IF abs(v_alloc-NEW.quantity)>0.000001 THEN
      RAISE EXCEPTION 'NCR physical disposition lot allocation mismatch';
    END IF;
  ELSE
    IF NEW.inventory_txn_id IS NOT NULL THEN RAISE EXCEPTION '% must not carry inventory transaction', NEW.disposition; END IF;
  END IF;
  NEW.decided_by := v_user.username;
  NEW.decided_at := transaction_timestamp();
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_disposition_before_insert_trg ON supplier_ncr_dispositions;
CREATE TRIGGER supplier_ncr_disposition_before_insert_trg
BEFORE INSERT ON supplier_ncr_dispositions
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_disposition_before_insert();

CREATE OR REPLACE FUNCTION supplier_ncr_disposition_after_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_ncr supplier_ncrs%ROWTYPE; v_old text;
BEGIN
  SELECT * INTO v_ncr FROM supplier_ncrs WHERE id=NEW.ncr_id FOR UPDATE;
  IF NEW.disposition='REWORK' THEN
    SELECT quality_status INTO v_old FROM lots WHERE id=v_ncr.lot_id FOR UPDATE;
    IF v_old <> 'HOLD' THEN
      UPDATE lots SET quality_status='HOLD' WHERE id=v_ncr.lot_id;
      INSERT INTO quality_status_history(lot_id, inspection_id, from_status, to_status,
        changed_by_user_id, changed_by, changed_at, source, source_ref, notes)
      VALUES(v_ncr.lot_id,NULL,v_old,'HOLD',NEW.decided_by_user_id,NEW.decided_by,NEW.decided_at,
        'NCR_DISPOSITION',NEW.id::text,'REWORK disposition for NCR '||v_ncr.ncr_no);
    END IF;
    UPDATE supplier_ncrs SET status='IN_REWORK' WHERE id=NEW.ncr_id;
  ELSIF NEW.disposition='USE_AS_IS' THEN
    SELECT quality_status INTO v_old FROM lots WHERE id=v_ncr.lot_id FOR UPDATE;
    IF v_old <> 'OK' THEN
      UPDATE lots SET quality_status='OK' WHERE id=v_ncr.lot_id;
      INSERT INTO quality_status_history(lot_id, inspection_id, from_status, to_status,
        changed_by_user_id, changed_by, changed_at, source, source_ref, notes)
      VALUES(v_ncr.lot_id,NULL,v_old,'OK',NEW.decided_by_user_id,NEW.decided_by,NEW.decided_at,
        'NCR_DISPOSITION',NEW.id::text,'USE_AS_IS disposition for NCR '||v_ncr.ncr_no);
    END IF;
    UPDATE supplier_ncrs SET status='CLOSED', closed_by_user_id=NEW.decided_by_user_id WHERE id=NEW.ncr_id;
  ELSE
    UPDATE supplier_ncrs SET status='CLOSED', closed_by_user_id=NEW.decided_by_user_id WHERE id=NEW.ncr_id;
  END IF;
  -- The NCR status UPDATE above is audited by supplier_ncr_after_update(),
  -- which recognizes the just-inserted disposition and emits one DISPOSITION_* event.
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_ncr_disposition_after_insert_trg ON supplier_ncr_dispositions;
CREATE TRIGGER supplier_ncr_disposition_after_insert_trg
AFTER INSERT ON supplier_ncr_dispositions
FOR EACH ROW EXECUTE FUNCTION supplier_ncr_disposition_after_insert();

CREATE OR REPLACE FUNCTION reject_supplier_quality_evidence_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION '% is append-only', TG_TABLE_NAME; END$$;
DROP TRIGGER IF EXISTS supplier_ncr_dispositions_append_only_trg ON supplier_ncr_dispositions;
CREATE TRIGGER supplier_ncr_dispositions_append_only_trg
BEFORE UPDATE OR DELETE ON supplier_ncr_dispositions
FOR EACH ROW EXECUTE FUNCTION reject_supplier_quality_evidence_mutation();
DROP TRIGGER IF EXISTS supplier_ncr_history_append_only_trg ON supplier_ncr_history;
CREATE TRIGGER supplier_ncr_history_append_only_trg
BEFORE UPDATE OR DELETE ON supplier_ncr_history
FOR EACH ROW EXECUTE FUNCTION reject_supplier_quality_evidence_mutation();

-- Override the inspection BEFORE trigger so an unrelated OPEN NCR cannot be
-- accidentally released by a PASS inspection. PASS after REWORK is allowed to
-- return the lot to OK only when no OPEN (not-yet-dispositioned) NCR remains.
CREATE OR REPLACE FUNCTION quality_inspection_before_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_username text;
  v_role text;
  v_active boolean;
  v_current_status text;
  v_lot_qty numeric;
BEGIN
  IF NEW.inspector_user_id IS NULL THEN
    RAISE EXCEPTION 'quality inspection requires inspector_user_id';
  END IF;
  SELECT username, role, is_active
    INTO v_username, v_role, v_active
    FROM users WHERE id=NEW.inspector_user_id;
  IF NOT FOUND OR NOT v_active THEN
    RAISE EXCEPTION 'quality inspector is inactive or does not exist';
  END IF;
  IF v_role NOT IN ('operator','planner','admin') THEN
    RAISE EXCEPTION 'user role % is not allowed to record quality inspections', v_role;
  END IF;
  SELECT quality_status, quantity INTO v_current_status, v_lot_qty
    FROM lots WHERE id=NEW.lot_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'quality lot % does not exist', NEW.lot_id; END IF;
  IF NEW.result NOT IN ('PASS','FAIL','HOLD') THEN RAISE EXCEPTION 'invalid quality result %', NEW.result; END IF;
  IF NEW.defect_qty < 0 OR NEW.defect_qty > v_lot_qty THEN
    RAISE EXCEPTION 'defect_qty % must be between 0 and lot quantity %', NEW.defect_qty, v_lot_qty;
  END IF;
  NEW.inspector := v_username;
  NEW.inspected_at := transaction_timestamp();
  NEW.previous_status := v_current_status;
  NEW.resulting_status := quality_status_for_result(NEW.result);
  IF NEW.result='PASS' AND EXISTS (
    SELECT 1 FROM supplier_ncrs n WHERE n.lot_id=NEW.lot_id AND n.status='OPEN'
  ) THEN
    NEW.resulting_status := 'HOLD';
  END IF;
  RETURN NEW;
END$$;

-- Automatic NCR from supplier FAIL inspection. The quality inspection trigger has
-- already set the lot REJECTED before this AFTER trigger executes (trigger names sort).
CREATE OR REPLACE FUNCTION auto_supplier_ncr_from_fail()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_lot lots%ROWTYPE;
  v_pr purchase_receipts%ROWTYPE;
  v_po uuid;
  v_balance numeric;
  v_no text;
BEGIN
  IF NEW.result <> 'FAIL' THEN RETURN NEW; END IF;
  SELECT * INTO v_lot FROM lots WHERE id=NEW.lot_id;
  IF NOT FOUND OR btrim(v_lot.supplier)='' THEN RETURN NEW; END IF;
  IF EXISTS(SELECT 1 FROM supplier_ncrs WHERE inspection_id=NEW.id) THEN RETURN NEW; END IF;
  SELECT * INTO v_pr FROM purchase_receipts WHERE lot_id=NEW.lot_id ORDER BY received_at DESC LIMIT 1;
  IF FOUND THEN v_po := v_pr.purchase_order_id; END IF;
  SELECT COALESCE(sum(quantity),0) INTO v_balance FROM lot_movements WHERE lot_id=NEW.lot_id;
  v_no := 'NCR-' || upper(substr(replace(NEW.id::text,'-',''),1,12));
  INSERT INTO supplier_ncrs(
    id,ncr_no,supplier,purchase_order_id,purchase_receipt_id,item_id,lot_id,inspection_id,
    affected_qty,severity,description,status,created_by_user_id,created_by,created_at
  ) VALUES (
    gen_random_uuid(),v_no,v_lot.supplier,v_po,CASE WHEN v_pr.id IS NULL THEN NULL ELSE v_pr.id END,
    v_lot.item_id,v_lot.id,NEW.id,
    CASE WHEN NEW.defect_qty>0 THEN NEW.defect_qty ELSE GREATEST(v_balance,0) END,
    'MAJOR',COALESCE(NULLIF(NEW.notes,''),'Supplier incoming inspection failed'),'OPEN',
    NEW.inspector_user_id,NEW.inspector,NEW.inspected_at
  );
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS supplier_quality_auto_ncr_after_inspection_trg ON quality_inspections;
CREATE TRIGGER supplier_quality_auto_ncr_after_inspection_trg
AFTER INSERT ON quality_inspections
FOR EACH ROW EXECUTE FUNCTION auto_supplier_ncr_from_fail();

-- Supplier quality scorecard. PPM uses inspected defect quantity / received quantity.
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
    FROM supplier_ncrs GROUP BY btrim(supplier)
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

-- Existing evidence must remain consistent.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM supplier_ncrs n JOIN lots l ON l.id=n.lot_id
     WHERE btrim(n.supplier)<>btrim(l.supplier) OR n.item_id<>l.item_id
  ) THEN
    RAISE EXCEPTION 'supplier quality migration blocked: NCR/lot identity mismatch';
  END IF;
END$$;
