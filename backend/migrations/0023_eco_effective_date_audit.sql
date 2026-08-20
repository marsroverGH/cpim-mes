-- ============================================================
-- 0023: ECO effective-date + approval/application audit guard
-- ============================================================
-- Guarantees for new transitions:
--   * ECOs are created only as DRAFT.
--   * Approved ECO content is immutable (header + component rows).
--   * DRAFT -> APPROVED -> APPLIED is the only application path.
--   * DRAFT/APPROVED may transition to CANCELLED; APPLIED is terminal.
--   * Approve/apply/cancel actors are real, active admin users and username/id match.
--   * APPLIED is rejected before effective_date in the configured business timezone.
--   * Approval/application/cancellation timestamps + actors are immutable.
--   * eco_status_history is append-only and populated by DB triggers, including direct SQL.
--
-- Business timezone: session GUC app.business_timezone, fallback Asia/Tokyo.

BEGIN;

ALTER TABLE engineering_changes
  ADD COLUMN IF NOT EXISTS requested_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS approved_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS approved_at          timestamptz,
  ADD COLUMN IF NOT EXISTS applied_by           text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS applied_by_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS cancelled_by         text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cancelled_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  ADD COLUMN IF NOT EXISTS cancelled_at         timestamptz;

-- Best-effort identity linking for legacy rows. Unknown historical actors are
-- explicitly labeled rather than fabricated as a current user.
UPDATE engineering_changes e
   SET requested_by_user_id = u.id
  FROM users u
 WHERE e.requested_by_user_id IS NULL
   AND e.requested_by <> ''
   AND u.username = e.requested_by;

UPDATE engineering_changes e
   SET approved_by_user_id = u.id
  FROM users u
 WHERE e.approved_by_user_id IS NULL
   AND e.approved_by <> ''
   AND u.username = e.approved_by;

UPDATE engineering_changes
   SET approved_by = CASE WHEN approved_by='' THEN 'LEGACY_UNKNOWN' ELSE approved_by END,
       approved_at = COALESCE(approved_at, applied_at, created_at)
 WHERE status IN ('APPROVED','APPLIED');

UPDATE engineering_changes
   SET applied_by = CASE WHEN applied_by='' THEN 'LEGACY_UNKNOWN' ELSE applied_by END,
       applied_at = COALESCE(applied_at, created_at)
 WHERE status='APPLIED';

UPDATE engineering_changes
   SET cancelled_by = CASE WHEN cancelled_by='' THEN 'LEGACY_UNKNOWN' ELSE cancelled_by END,
       cancelled_at = COALESCE(cancelled_at, created_at)
 WHERE status='CANCELLED';

CREATE OR REPLACE FUNCTION eco_business_timezone()
RETURNS text
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
  tz text;
BEGIN
  tz := current_setting('app.business_timezone', true);
  IF tz IS NULL OR btrim(tz) = '' THEN
    tz := 'Asia/Tokyo';
  END IF;
  -- Raises immediately for an invalid timezone name.
  PERFORM now() AT TIME ZONE tz;
  RETURN tz;
END;
$$;

CREATE OR REPLACE FUNCTION eco_business_date(ts timestamptz)
RETURNS date
LANGUAGE sql
STABLE
AS $$
  SELECT (ts AT TIME ZONE eco_business_timezone())::date
$$;

-- Existing APPLIED history that predates its own effective date is unsafe.
-- Do not silently rewrite it: stop migration and require explicit reconciliation.
DO $$
DECLARE bad record;
BEGIN
  SELECT id, eco_no, effective_date, applied_at
    INTO bad
    FROM engineering_changes
   WHERE status='APPLIED'
     AND eco_business_date(applied_at) < effective_date
   LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION
      '0023 found legacy ECO % (%) applied on % before effective date %; reconcile before migration',
      bad.eco_no, bad.id, eco_business_date(bad.applied_at), bad.effective_date;
  END IF;
END$$;

CREATE TABLE IF NOT EXISTS eco_status_history (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  eco_id                  uuid NOT NULL REFERENCES engineering_changes(id) ON DELETE RESTRICT,
  from_status             text NOT NULL,
  to_status               text NOT NULL,
  actor_user_id           uuid REFERENCES users(id) ON DELETE RESTRICT,
  actor_username          text NOT NULL DEFAULT '',
  occurred_at             timestamptz NOT NULL,
  effective_date_snapshot date NOT NULL,
  audit_source            text NOT NULL DEFAULT 'DB_TRIGGER'
    CHECK (audit_source IN ('DB_TRIGGER','LEGACY_RECONSTRUCTED'))
);

CREATE INDEX IF NOT EXISTS eco_status_history_eco_idx
  ON eco_status_history(eco_id, occurred_at, id);

-- Reconstruct legacy history only when a row has no history yet.
INSERT INTO eco_status_history
  (eco_id, from_status, to_status, actor_user_id, actor_username,
   occurred_at, effective_date_snapshot, audit_source)
SELECT e.id, '', 'DRAFT', e.requested_by_user_id, e.requested_by,
       e.created_at, e.effective_date, 'LEGACY_RECONSTRUCTED'
  FROM engineering_changes e
 WHERE NOT EXISTS (SELECT 1 FROM eco_status_history h WHERE h.eco_id=e.id);

INSERT INTO eco_status_history
  (eco_id, from_status, to_status, actor_user_id, actor_username,
   occurred_at, effective_date_snapshot, audit_source)
SELECT e.id, 'DRAFT', 'APPROVED', e.approved_by_user_id, e.approved_by,
       e.approved_at, e.effective_date, 'LEGACY_RECONSTRUCTED'
  FROM engineering_changes e
 WHERE e.status IN ('APPROVED','APPLIED')
   AND NOT EXISTS (
     SELECT 1 FROM eco_status_history h
      WHERE h.eco_id=e.id AND h.to_status='APPROVED'
   );

INSERT INTO eco_status_history
  (eco_id, from_status, to_status, actor_user_id, actor_username,
   occurred_at, effective_date_snapshot, audit_source)
SELECT e.id, 'APPROVED', 'APPLIED', e.applied_by_user_id, e.applied_by,
       e.applied_at, e.effective_date, 'LEGACY_RECONSTRUCTED'
  FROM engineering_changes e
 WHERE e.status='APPLIED'
   AND NOT EXISTS (
     SELECT 1 FROM eco_status_history h
      WHERE h.eco_id=e.id AND h.to_status='APPLIED'
   );

INSERT INTO eco_status_history
  (eco_id, from_status, to_status, actor_user_id, actor_username,
   occurred_at, effective_date_snapshot, audit_source)
SELECT e.id, 'UNKNOWN', 'CANCELLED', e.cancelled_by_user_id, e.cancelled_by,
       e.cancelled_at, e.effective_date, 'LEGACY_RECONSTRUCTED'
  FROM engineering_changes e
 WHERE e.status='CANCELLED'
   AND NOT EXISTS (
     SELECT 1 FROM eco_status_history h
      WHERE h.eco_id=e.id AND h.to_status='CANCELLED'
   );

CREATE OR REPLACE FUNCTION assert_current_eco_admin(actor_id uuid, actor_name text)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE u record;
BEGIN
  IF actor_id IS NULL OR actor_name IS NULL OR btrim(actor_name)='' THEN
    RAISE EXCEPTION 'ECO transition actor id and username are required';
  END IF;
  SELECT id, username, role, is_active INTO u FROM users WHERE id=actor_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'ECO transition actor % does not exist', actor_id;
  END IF;
  IF u.username <> actor_name THEN
    RAISE EXCEPTION 'ECO transition actor username mismatch for user %', actor_id;
  END IF;
  IF NOT u.is_active THEN
    RAISE EXCEPTION 'ECO transition actor % is inactive', actor_name;
  END IF;
  IF u.role <> 'admin' THEN
    RAISE EXCEPTION 'ECO transition actor % must be admin (role=%)', actor_name, u.role;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION guard_engineering_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.status <> 'DRAFT' THEN
      RAISE EXCEPTION 'new ECO must be DRAFT';
    END IF;
    IF NEW.approved_at IS NOT NULL OR NEW.applied_at IS NOT NULL OR NEW.cancelled_at IS NOT NULL
       OR NEW.approved_by <> '' OR NEW.applied_by <> '' OR NEW.cancelled_by <> '' THEN
      RAISE EXCEPTION 'new DRAFT ECO cannot contain approval/application/cancellation audit fields';
    END IF;
    RETURN NEW;
  END IF;

  -- Approved/cancelled/applied business content and audit facts are immutable.
  IF OLD.status <> 'DRAFT' THEN
    IF (NEW.eco_no, NEW.title, NEW.description, NEW.effective_date, NEW.requested_by, NEW.requested_by_user_id)
       IS DISTINCT FROM
       (OLD.eco_no, OLD.title, OLD.description, OLD.effective_date, OLD.requested_by, OLD.requested_by_user_id) THEN
      RAISE EXCEPTION 'ECO header is immutable after leaving DRAFT';
    END IF;
  END IF;

  IF NEW.status = OLD.status THEN
    IF (NEW.approved_by, NEW.approved_by_user_id, NEW.approved_at,
        NEW.applied_by, NEW.applied_by_user_id, NEW.applied_at,
        NEW.cancelled_by, NEW.cancelled_by_user_id, NEW.cancelled_at)
       IS DISTINCT FROM
       (OLD.approved_by, OLD.approved_by_user_id, OLD.approved_at,
        OLD.applied_by, OLD.applied_by_user_id, OLD.applied_at,
        OLD.cancelled_by, OLD.cancelled_by_user_id, OLD.cancelled_at) THEN
      RAISE EXCEPTION 'ECO audit fields cannot be edited without a valid status transition';
    END IF;
    RETURN NEW;
  END IF;

  IF NOT (
       (OLD.status='DRAFT'    AND NEW.status IN ('APPROVED','CANCELLED'))
    OR (OLD.status='APPROVED' AND NEW.status IN ('APPLIED','CANCELLED'))
  ) THEN
    RAISE EXCEPTION 'invalid ECO status transition: % -> %', OLD.status, NEW.status;
  END IF;

  IF NEW.status='APPROVED' THEN
    PERFORM assert_current_eco_admin(NEW.approved_by_user_id, NEW.approved_by);
    IF NEW.approved_at IS NULL OR NEW.approved_at IS DISTINCT FROM now() THEN
      RAISE EXCEPTION 'ECO approved_at must be the current transaction timestamp';
    END IF;
    IF NEW.applied_at IS NOT NULL OR NEW.cancelled_at IS NOT NULL THEN
      RAISE EXCEPTION 'APPROVED ECO cannot contain application/cancellation timestamps';
    END IF;
  ELSIF NEW.status='APPLIED' THEN
    PERFORM assert_current_eco_admin(NEW.applied_by_user_id, NEW.applied_by);
    IF NEW.approved_at IS NULL OR NEW.approved_by='' THEN
      RAISE EXCEPTION 'APPLIED ECO requires prior approval audit fields';
    END IF;
    IF NEW.applied_at IS NULL OR NEW.applied_at IS DISTINCT FROM now() THEN
      RAISE EXCEPTION 'ECO applied_at must be the current transaction timestamp';
    END IF;
    IF NEW.applied_at < NEW.approved_at THEN
      RAISE EXCEPTION 'ECO applied_at cannot precede approved_at';
    END IF;
    IF eco_business_date(NEW.applied_at) < NEW.effective_date THEN
      RAISE EXCEPTION 'ECO % cannot be applied on business date % before effective date %',
        NEW.eco_no, eco_business_date(NEW.applied_at), NEW.effective_date;
    END IF;
  ELSIF NEW.status='CANCELLED' THEN
    PERFORM assert_current_eco_admin(NEW.cancelled_by_user_id, NEW.cancelled_by);
    IF NEW.cancelled_at IS NULL OR NEW.cancelled_at IS DISTINCT FROM now() THEN
      RAISE EXCEPTION 'ECO cancelled_at must be the current transaction timestamp';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS engineering_change_guard_trg ON engineering_changes;
CREATE TRIGGER engineering_change_guard_trg
BEFORE INSERT OR UPDATE ON engineering_changes
FOR EACH ROW EXECUTE FUNCTION guard_engineering_change();

CREATE OR REPLACE FUNCTION guard_eco_component_draft_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE target_eco uuid; st text;
BEGIN
  target_eco := CASE WHEN TG_OP='DELETE' THEN OLD.eco_id ELSE NEW.eco_id END;
  SELECT status INTO st FROM engineering_changes WHERE id=target_eco FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'ECO % does not exist', target_eco; END IF;
  IF st <> 'DRAFT' THEN
    RAISE EXCEPTION 'ECO component rows are immutable after approval (status=%)', st;
  END IF;
  IF TG_OP='DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS eco_component_draft_only_trg ON eco_components;
CREATE TRIGGER eco_component_draft_only_trg
BEFORE INSERT OR UPDATE OR DELETE ON eco_components
FOR EACH ROW EXECUTE FUNCTION guard_eco_component_draft_only();

CREATE OR REPLACE FUNCTION append_eco_status_history()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE actor_id uuid; actor_name text; event_at timestamptz;
BEGIN
  IF TG_OP='INSERT' THEN
    INSERT INTO eco_status_history
      (eco_id, from_status, to_status, actor_user_id, actor_username,
       occurred_at, effective_date_snapshot, audit_source)
    VALUES
      (NEW.id, '', 'DRAFT', NEW.requested_by_user_id, NEW.requested_by,
       NEW.created_at, NEW.effective_date, 'DB_TRIGGER');
    RETURN NEW;
  END IF;
  IF NEW.status = OLD.status THEN RETURN NEW; END IF;
  IF NEW.status='APPROVED' THEN
    actor_id:=NEW.approved_by_user_id; actor_name:=NEW.approved_by; event_at:=NEW.approved_at;
  ELSIF NEW.status='APPLIED' THEN
    actor_id:=NEW.applied_by_user_id; actor_name:=NEW.applied_by; event_at:=NEW.applied_at;
  ELSIF NEW.status='CANCELLED' THEN
    actor_id:=NEW.cancelled_by_user_id; actor_name:=NEW.cancelled_by; event_at:=NEW.cancelled_at;
  ELSE
    RAISE EXCEPTION 'cannot record unsupported ECO transition to %', NEW.status;
  END IF;
  INSERT INTO eco_status_history
    (eco_id, from_status, to_status, actor_user_id, actor_username,
     occurred_at, effective_date_snapshot, audit_source)
  VALUES
    (NEW.id, OLD.status, NEW.status, actor_id, actor_name,
     event_at, NEW.effective_date, 'DB_TRIGGER');
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS eco_status_history_append_trg ON engineering_changes;
CREATE TRIGGER eco_status_history_append_trg
AFTER INSERT OR UPDATE OF status ON engineering_changes
FOR EACH ROW EXECUTE FUNCTION append_eco_status_history();

CREATE OR REPLACE FUNCTION reject_eco_history_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'eco_status_history is append-only';
END;
$$;
DROP TRIGGER IF EXISTS eco_history_immutable_trg ON eco_status_history;
CREATE TRIGGER eco_history_immutable_trg
BEFORE UPDATE OR DELETE ON eco_status_history
FOR EACH ROW EXECUTE FUNCTION reject_eco_history_mutation();

CREATE OR REPLACE VIEW v_eco_audit_reconciliation AS
SELECT e.id, e.eco_no, e.status, e.effective_date,
       e.requested_by, e.approved_by, e.approved_at,
       e.applied_by, e.applied_at, e.cancelled_by, e.cancelled_at,
       (SELECT count(*) FROM eco_status_history h WHERE h.eco_id=e.id) AS history_count,
       CASE
         WHEN e.status='DRAFT' THEN e.approved_at IS NULL AND e.applied_at IS NULL AND e.cancelled_at IS NULL
         WHEN e.status='APPROVED' THEN e.approved_at IS NOT NULL AND e.approved_by_user_id IS NOT NULL AND e.applied_at IS NULL AND e.cancelled_at IS NULL
         WHEN e.status='APPLIED' THEN e.approved_at IS NOT NULL AND e.approved_by_user_id IS NOT NULL
              AND e.applied_at IS NOT NULL AND e.applied_by_user_id IS NOT NULL
              AND eco_business_date(e.applied_at) >= e.effective_date
         WHEN e.status='CANCELLED' THEN e.cancelled_at IS NOT NULL AND e.cancelled_by_user_id IS NOT NULL
         ELSE false
       END AS is_consistent
  FROM engineering_changes e;

COMMIT;
