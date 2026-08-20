-- ============================================================
-- 0021: Strict Shop Floor operation state machine
-- ============================================================
-- State model:
--   PENDING -> READY -> IN_PROGRESS <-> PAUSED -> COMPLETED
--
-- Invariants:
--   * only the first unfinished operation may be READY/IN_PROGRESS/PAUSED
--   * a successor cannot START before all predecessors are COMPLETED
--   * only one READY/IN_PROGRESS/PAUSED operation exists per WO
--   * active session time is server-owned and exists only while IN_PROGRESS
--   * new operation logs identify a real users row and matching username
--   * sequence invariants are checked DEFERRABLY at COMMIT so COMPLETE can
--     atomically make the current operation COMPLETED and its successor READY.

BEGIN;

ALTER TABLE wo_operations
  DROP CONSTRAINT IF EXISTS wo_operations_status_check;

ALTER TABLE wo_operations
  ADD CONSTRAINT wo_operations_status_check
  CHECK (status IN ('PENDING','READY','IN_PROGRESS','PAUSED','COMPLETED'));

ALTER TABLE wo_operations
  ADD COLUMN IF NOT EXISTS active_started_at timestamptz,
  ADD COLUMN IF NOT EXISTS operator_user_id uuid;

ALTER TABLE operation_logs
  ADD COLUMN IF NOT EXISTS operator_user_id uuid;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname='wo_operations_operator_user_id_fkey'
       AND conrelid='wo_operations'::regclass
  ) THEN
    ALTER TABLE wo_operations
      ADD CONSTRAINT wo_operations_operator_user_id_fkey
      FOREIGN KEY (operator_user_id) REFERENCES users(id) ON DELETE RESTRICT;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conname='operation_logs_operator_user_id_fkey'
       AND conrelid='operation_logs'::regclass
  ) THEN
    ALTER TABLE operation_logs
      ADD CONSTRAINT operation_logs_operator_user_id_fkey
      FOREIGN KEY (operator_user_id) REFERENCES users(id) ON DELETE RESTRICT;
  END IF;
END$$;

-- Existing IN_PROGRESS rows predate active_started_at. Use the original start
-- timestamp as the best available beginning of the active session.
UPDATE wo_operations
   SET active_started_at=COALESCE(active_started_at, started_at, now())
 WHERE status='IN_PROGRESS'
   AND active_started_at IS NULL;

-- Refuse to hide already-invalid legacy sequencing. Operators must reconcile
-- these rows explicitly before the migration is retried.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
      FROM wo_operations o
     WHERE o.status IN ('IN_PROGRESS','COMPLETED')
       AND EXISTS (
         SELECT 1 FROM wo_operations p
          WHERE p.wo_id=o.wo_id
            AND p.seq_no<o.seq_no
            AND p.status<>'COMPLETED'
       )
  ) THEN
    RAISE EXCEPTION
      '0021 cannot migrate: a started/completed operation has an unfinished predecessor';
  END IF;

  IF EXISTS (
    SELECT wo_id
      FROM wo_operations
     WHERE status='IN_PROGRESS'
     GROUP BY wo_id
    HAVING COUNT(*)>1
  ) THEN
    RAISE EXCEPTION
      '0021 cannot migrate: a work order has multiple IN_PROGRESS operations';
  END IF;
END$$;

-- For released/in-progress legacy WOs with no active operation, promote the
-- first unfinished operation to READY. Successors remain PENDING.
WITH eligible AS (
  SELECT o.id
    FROM wo_operations o
    JOIN work_orders w ON w.id=o.wo_id
   WHERE o.status='PENDING'
     AND w.status IN ('RELEASED','IN_PROGRESS')
     AND NOT EXISTS (
       SELECT 1 FROM wo_operations a
        WHERE a.wo_id=o.wo_id
          AND a.status='IN_PROGRESS'
     )
     AND NOT EXISTS (
       SELECT 1 FROM wo_operations p
        WHERE p.wo_id=o.wo_id
          AND p.seq_no<o.seq_no
          AND p.status<>'COMPLETED'
     )
)
UPDATE wo_operations o
   SET status='READY'
  FROM eligible e
 WHERE o.id=e.id;

CREATE UNIQUE INDEX IF NOT EXISTS wo_operations_one_active_step_uq
  ON wo_operations(wo_id)
  WHERE status IN ('READY','IN_PROGRESS','PAUSED');

ALTER TABLE wo_operations
  DROP CONSTRAINT IF EXISTS wo_operations_active_session_check;
ALTER TABLE wo_operations
  ADD CONSTRAINT wo_operations_active_session_check
  CHECK (
    (status='IN_PROGRESS' AND active_started_at IS NOT NULL)
    OR
    (status<>'IN_PROGRESS' AND active_started_at IS NULL)
  );

CREATE OR REPLACE FUNCTION enforce_wo_operation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  planned_qty numeric;
  db_username text;
BEGIN
  SELECT quantity INTO planned_qty FROM work_orders WHERE id=NEW.wo_id;
  IF planned_qty IS NULL THEN
    RAISE EXCEPTION 'work order % not found for operation %', NEW.wo_id, NEW.id
      USING ERRCODE='23514';
  END IF;

  IF NEW.completed_qty < OLD.completed_qty THEN
    RAISE EXCEPTION 'operation cumulative completed quantity cannot decrease (% -> %)',
      OLD.completed_qty, NEW.completed_qty USING ERRCODE='23514';
  END IF;
  IF NEW.completed_qty > planned_qty THEN
    RAISE EXCEPTION 'operation completed quantity % exceeds WO planned quantity %',
      NEW.completed_qty, planned_qty USING ERRCODE='23514';
  END IF;

  IF NEW.status IS DISTINCT FROM OLD.status THEN
    IF NOT (
      (OLD.status='PENDING'     AND NEW.status='READY') OR
      (OLD.status='READY'       AND NEW.status='IN_PROGRESS') OR
      (OLD.status='IN_PROGRESS' AND NEW.status='PAUSED') OR
      (OLD.status='PAUSED'      AND NEW.status='IN_PROGRESS') OR
      (OLD.status='IN_PROGRESS' AND NEW.status='COMPLETED')
    ) THEN
      RAISE EXCEPTION 'invalid Shop Floor state transition: % -> %', OLD.status, NEW.status
        USING ERRCODE='23514';
    END IF;
  END IF;

  IF NEW.status IN ('READY','IN_PROGRESS') AND EXISTS (
    SELECT 1 FROM wo_operations p
     WHERE p.wo_id=NEW.wo_id
       AND p.seq_no<NEW.seq_no
       AND p.status<>'COMPLETED'
  ) THEN
    RAISE EXCEPTION 'operation % cannot become % before all predecessors are COMPLETED',
      NEW.seq_no, NEW.status USING ERRCODE='23514';
  END IF;

  IF NEW.status='COMPLETED' THEN
    IF NEW.completed_qty < planned_qty THEN
      RAISE EXCEPTION 'operation cannot become COMPLETED before cumulative quantity reaches WO planned quantity'
        USING ERRCODE='23514';
    END IF;
    IF NEW.completed_at IS NULL THEN
      RAISE EXCEPTION 'COMPLETED operation must have completed_at'
        USING ERRCODE='23514';
    END IF;
  END IF;

  IF NEW.operator_user_id IS NOT NULL THEN
    SELECT username INTO db_username FROM users WHERE id=NEW.operator_user_id;
    IF db_username IS NULL OR db_username IS DISTINCT FROM NEW.operator THEN
      RAISE EXCEPTION 'operation operator identity does not match users table'
        USING ERRCODE='23514';
    END IF;
  END IF;

  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_wo_operation_transition ON wo_operations;
CREATE TRIGGER trg_wo_operation_transition
BEFORE UPDATE ON wo_operations
FOR EACH ROW EXECUTE FUNCTION enforce_wo_operation_transition();

CREATE OR REPLACE FUNCTION assert_wo_operation_sequence(p_wo_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  wo_status text;
  first_unfinished text;
BEGIN
  SELECT status INTO wo_status FROM work_orders WHERE id=p_wo_id;
  IF wo_status IS NULL THEN
    RETURN;
  END IF;

  IF (SELECT COUNT(*) FROM wo_operations
       WHERE wo_id=p_wo_id AND status IN ('READY','IN_PROGRESS','PAUSED')) > 1 THEN
    RAISE EXCEPTION 'WO % has more than one active/ready Shop Floor operation', p_wo_id
      USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM wo_operations o
     WHERE o.wo_id=p_wo_id
       AND o.status='COMPLETED'
       AND EXISTS (
         SELECT 1 FROM wo_operations p
          WHERE p.wo_id=o.wo_id AND p.seq_no<o.seq_no AND p.status<>'COMPLETED'
       )
  ) THEN
    RAISE EXCEPTION 'WO % has a completed operation after an unfinished predecessor', p_wo_id
      USING ERRCODE='23514';
  END IF;

  IF EXISTS (
    SELECT 1 FROM wo_operations o
     WHERE o.wo_id=p_wo_id
       AND o.status IN ('READY','IN_PROGRESS','PAUSED')
       AND EXISTS (
         SELECT 1 FROM wo_operations p
          WHERE p.wo_id=o.wo_id AND p.seq_no<o.seq_no AND p.status<>'COMPLETED'
       )
  ) THEN
    RAISE EXCEPTION 'WO % has an active/ready operation before its predecessor is completed', p_wo_id
      USING ERRCODE='23514';
  END IF;

  IF wo_status IN ('RELEASED','IN_PROGRESS') THEN
    SELECT status INTO first_unfinished
      FROM wo_operations
     WHERE wo_id=p_wo_id AND status<>'COMPLETED'
     ORDER BY seq_no
     LIMIT 1;
    IF first_unfinished='PENDING' THEN
      RAISE EXCEPTION 'WO % first unfinished operation must be READY, IN_PROGRESS or PAUSED', p_wo_id
        USING ERRCODE='23514';
    END IF;
  END IF;
END$$;

CREATE OR REPLACE FUNCTION deferred_wo_operation_sequence_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    PERFORM assert_wo_operation_sequence(OLD.wo_id);
  ELSIF TG_OP='UPDATE' AND OLD.wo_id IS DISTINCT FROM NEW.wo_id THEN
    PERFORM assert_wo_operation_sequence(OLD.wo_id);
    PERFORM assert_wo_operation_sequence(NEW.wo_id);
  ELSE
    PERFORM assert_wo_operation_sequence(NEW.wo_id);
  END IF;
  RETURN NULL;
END$$;

DROP TRIGGER IF EXISTS trg_wo_operation_sequence_deferred ON wo_operations;
CREATE CONSTRAINT TRIGGER trg_wo_operation_sequence_deferred
AFTER INSERT OR UPDATE OR DELETE ON wo_operations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION deferred_wo_operation_sequence_guard();

CREATE OR REPLACE FUNCTION enforce_operation_log_actor()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  db_username text;
BEGIN
  IF NEW.operator_user_id IS NULL THEN
    RAISE EXCEPTION 'new Shop Floor operation log must identify operator_user_id'
      USING ERRCODE='23514';
  END IF;
  SELECT username INTO db_username FROM users WHERE id=NEW.operator_user_id;
  IF db_username IS NULL OR db_username IS DISTINCT FROM NEW.operator THEN
    RAISE EXCEPTION 'operation log operator identity does not match users table'
      USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS trg_operation_log_actor ON operation_logs;
CREATE TRIGGER trg_operation_log_actor
BEFORE INSERT ON operation_logs
FOR EACH ROW EXECUTE FUNCTION enforce_operation_log_actor();

COMMIT;
