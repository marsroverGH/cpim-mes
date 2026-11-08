-- 0044: audit_log is append-only.
--
-- The audit trail is evidence, like eco_status_history and quality_status_history:
-- rows may be added but never changed or removed. Row triggers do not fire for
-- TRUNCATE, so a statement trigger covers it.
--
-- This stops mistakes and SQL issued through the application. A database
-- superuser can still disable the triggers.

CREATE OR REPLACE FUNCTION reject_audit_log_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'audit_log is append-only; % is not allowed', TG_OP
    USING ERRCODE = '23514';
END$$;

DROP TRIGGER IF EXISTS audit_log_append_only_trg ON audit_log;
CREATE TRIGGER audit_log_append_only_trg
BEFORE UPDATE OR DELETE ON audit_log
FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation();

DROP TRIGGER IF EXISTS audit_log_no_truncate_trg ON audit_log;
CREATE TRIGGER audit_log_no_truncate_trg
BEFORE TRUNCATE ON audit_log
FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_log_mutation();
