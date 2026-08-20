-- ============================================================
-- 0024: Quality inspection transaction + immutable lot status audit
-- ============================================================
-- Quality inspections are authoritative quality events. For every new inspection:
--   1) the target lot is locked,
--   2) authenticated inspector identity is validated,
--   3) previous/resulting quality statuses are captured,
--   4) lots.quality_status is changed,
--   5) an immutable quality_status_history row is appended,
-- all inside the caller's single database transaction.

ALTER TABLE quality_inspections
  ADD COLUMN IF NOT EXISTS inspector_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS previous_status text,
  ADD COLUMN IF NOT EXISTS resulting_status text;

-- Backfill identities where the historical username is still resolvable.
UPDATE quality_inspections qi
   SET inspector_user_id = u.id
  FROM users u
 WHERE qi.inspector_user_id IS NULL
   AND qi.inspector <> ''
   AND u.username = qi.inspector;

-- Every historic inspection has an unambiguous resulting status.
UPDATE quality_inspections
   SET resulting_status = CASE result
                            WHEN 'PASS' THEN 'OK'
                            WHEN 'FAIL' THEN 'REJECTED'
                            WHEN 'HOLD' THEN 'HOLD'
                          END
 WHERE resulting_status IS NULL;

-- Reconstruct previous status from preceding inspection where possible.
WITH ordered AS (
  SELECT id,
         lag(resulting_status) OVER (
           PARTITION BY lot_id ORDER BY inspected_at, id
         ) AS prev_status
    FROM quality_inspections
)
UPDATE quality_inspections qi
   SET previous_status = o.prev_status
  FROM ordered o
 WHERE qi.id = o.id
   AND qi.previous_status IS NULL;

ALTER TABLE quality_inspections
  DROP CONSTRAINT IF EXISTS quality_inspections_previous_status_check,
  DROP CONSTRAINT IF EXISTS quality_inspections_resulting_status_check;

ALTER TABLE quality_inspections
  ADD CONSTRAINT quality_inspections_previous_status_check
    CHECK (previous_status IS NULL OR previous_status IN ('OK','HOLD','REJECTED')),
  ADD CONSTRAINT quality_inspections_resulting_status_check
    CHECK (resulting_status IN ('OK','HOLD','REJECTED'));

ALTER TABLE quality_inspections
  ALTER COLUMN resulting_status SET NOT NULL;

CREATE TABLE IF NOT EXISTS quality_status_history (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  lot_id             uuid NOT NULL REFERENCES lots(id) ON DELETE RESTRICT,
  inspection_id      uuid NOT NULL UNIQUE REFERENCES quality_inspections(id) ON DELETE RESTRICT,
  from_status        text CHECK (from_status IS NULL OR from_status IN ('OK','HOLD','REJECTED')),
  to_status          text NOT NULL CHECK (to_status IN ('OK','HOLD','REJECTED')),
  changed_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  changed_by         text NOT NULL DEFAULT '',
  changed_at         timestamptz NOT NULL DEFAULT now(),
  source             text NOT NULL DEFAULT 'INSPECTION',
  notes              text NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS quality_status_history_lot_idx
  ON quality_status_history(lot_id, changed_at DESC, id DESC);

-- Backfill one audit row per legacy inspection. First historic transition may have
-- unknown from_status because pre-0024 state changes were not themselves audited.
INSERT INTO quality_status_history(
  lot_id, inspection_id, from_status, to_status,
  changed_by_user_id, changed_by, changed_at, source, notes
)
SELECT qi.lot_id, qi.id, qi.previous_status, qi.resulting_status,
       qi.inspector_user_id, qi.inspector, qi.inspected_at,
       'LEGACY_RECONSTRUCTED', qi.notes
  FROM quality_inspections qi
ON CONFLICT (inspection_id) DO NOTHING;

-- Existing data must already agree with the latest inspection. Do not silently
-- rewrite inconsistent production history during migration.
DO $$
DECLARE
  rec record;
BEGIN
  FOR rec IN
    SELECT DISTINCT ON (qi.lot_id)
           qi.lot_id, qi.resulting_status AS expected_status,
           l.quality_status AS actual_status
      FROM quality_inspections qi
      JOIN lots l ON l.id = qi.lot_id
     ORDER BY qi.lot_id, qi.inspected_at DESC, qi.id DESC
  LOOP
    IF rec.expected_status IS DISTINCT FROM rec.actual_status THEN
      RAISE EXCEPTION
        'quality migration blocked: lot % current status % disagrees with latest inspection status %',
        rec.lot_id, rec.actual_status, rec.expected_status;
    END IF;
  END LOOP;
END$$;

CREATE OR REPLACE FUNCTION quality_status_for_result(p_result text)
RETURNS text
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT CASE p_result
           WHEN 'PASS' THEN 'OK'
           WHEN 'FAIL' THEN 'REJECTED'
           WHEN 'HOLD' THEN 'HOLD'
           ELSE NULL
         END
$$;

-- Validate/normalize a new inspection while holding the lot row lock. New
-- inspection identity/time is server/DB authoritative and cannot be forged by
-- request payload fields.
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
    FROM users
   WHERE id = NEW.inspector_user_id;

  IF NOT FOUND OR NOT v_active THEN
    RAISE EXCEPTION 'quality inspector is inactive or does not exist';
  END IF;
  IF v_role NOT IN ('operator','planner','admin') THEN
    RAISE EXCEPTION 'user role % is not allowed to record quality inspections', v_role;
  END IF;

  SELECT quality_status, quantity
    INTO v_current_status, v_lot_qty
    FROM lots
   WHERE id = NEW.lot_id
   FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'quality lot % does not exist', NEW.lot_id;
  END IF;

  IF NEW.result NOT IN ('PASS','FAIL','HOLD') THEN
    RAISE EXCEPTION 'invalid quality result %', NEW.result;
  END IF;
  IF NEW.defect_qty < 0 OR NEW.defect_qty > v_lot_qty THEN
    RAISE EXCEPTION 'defect_qty % must be between 0 and lot quantity %', NEW.defect_qty, v_lot_qty;
  END IF;

  NEW.inspector := v_username;
  NEW.inspected_at := transaction_timestamp();
  NEW.previous_status := v_current_status;
  NEW.resulting_status := quality_status_for_result(NEW.result);
  RETURN NEW;
END$$;

CREATE OR REPLACE FUNCTION quality_inspection_after_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  UPDATE lots
     SET quality_status = NEW.resulting_status
   WHERE id = NEW.lot_id;

  INSERT INTO quality_status_history(
    lot_id, inspection_id, from_status, to_status,
    changed_by_user_id, changed_by, changed_at, source, notes
  ) VALUES (
    NEW.lot_id, NEW.id, NEW.previous_status, NEW.resulting_status,
    NEW.inspector_user_id, NEW.inspector, NEW.inspected_at, 'INSPECTION', NEW.notes
  );
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS quality_inspection_before_insert_trg ON quality_inspections;
CREATE TRIGGER quality_inspection_before_insert_trg
BEFORE INSERT ON quality_inspections
FOR EACH ROW EXECUTE FUNCTION quality_inspection_before_insert();

DROP TRIGGER IF EXISTS quality_inspection_after_insert_trg ON quality_inspections;
CREATE TRIGGER quality_inspection_after_insert_trg
AFTER INSERT ON quality_inspections
FOR EACH ROW EXECUTE FUNCTION quality_inspection_after_insert();

-- Quality records are evidence, not mutable master data.
CREATE OR REPLACE FUNCTION reject_quality_evidence_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION '% is append-only; corrections require a new inspection', TG_TABLE_NAME;
END$$;

DROP TRIGGER IF EXISTS quality_inspections_append_only_trg ON quality_inspections;
CREATE TRIGGER quality_inspections_append_only_trg
BEFORE UPDATE OR DELETE ON quality_inspections
FOR EACH ROW EXECUTE FUNCTION reject_quality_evidence_mutation();

DROP TRIGGER IF EXISTS quality_status_history_append_only_trg ON quality_status_history;
CREATE TRIGGER quality_status_history_append_only_trg
BEFORE UPDATE OR DELETE ON quality_status_history
FOR EACH ROW EXECUTE FUNCTION reject_quality_evidence_mutation();

-- Application-visible lot quality changes must originate from an inspection
-- trigger. pg_trigger_depth() is 2 when lots is updated by the inspection AFTER
-- trigger, and 1 for direct UPDATE statements.
CREATE OR REPLACE FUNCTION guard_direct_lot_quality_status_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.quality_status IS DISTINCT FROM OLD.quality_status
     AND pg_trigger_depth() <= 1 THEN
    RAISE EXCEPTION
      'lots.quality_status is inspection-controlled; record a quality inspection instead';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS lots_quality_status_guard_trg ON lots;
CREATE TRIGGER lots_quality_status_guard_trg
BEFORE UPDATE OF quality_status ON lots
FOR EACH ROW EXECUTE FUNCTION guard_direct_lot_quality_status_update();
