-- 0042 Controlled Recovery Plan Execution
--
-- 0041 deliberately keeps Simulation and Publish side-effect free. 0042 adds a
-- separate, auditable execution boundary. A published Recovery Scenario may be
-- prepared, preflighted, approved by a different administrator, and then
-- executed against operational records. No 0041 evidence row is rewritten.

CREATE TABLE recovery_plan_executions (
    id                      uuid PRIMARY KEY,
    scenario_id             uuid NOT NULL REFERENCES recovery_scenarios(id) ON DELETE RESTRICT,
    publication_id          uuid NOT NULL REFERENCES recovery_scenario_publications(id) ON DELETE RESTRICT,
    run_id                  uuid NOT NULL REFERENCES recovery_scenario_runs(id) ON DELETE RESTRICT,
    idempotency_key         text NOT NULL,
    status                  text NOT NULL DEFAULT 'PREPARED',

    publication_hash        text NOT NULL,
    run_result_hash         text NOT NULL,
    action_set_hash         text NOT NULL,
    preflight_hash          text,
    approval_hash           text,
    execution_result_hash   text,

    prepare_comment         text NOT NULL DEFAULT '',
    approval_comment        text NOT NULL DEFAULT '',
    failure_reason          text NOT NULL DEFAULT '',

    action_count            integer NOT NULL DEFAULT 0,
    succeeded_action_count  integer NOT NULL DEFAULT 0,
    failed_action_count     integer NOT NULL DEFAULT 0,

    prepared_by_user_id     uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    prepared_by_username    text NOT NULL,
    prepared_at             timestamptz NOT NULL DEFAULT now(),
    preflighted_at          timestamptz,

    approved_by_user_id     uuid REFERENCES users(id) ON DELETE RESTRICT,
    approved_by_username    text,
    approved_at             timestamptz,

    executed_by_user_id     uuid REFERENCES users(id) ON DELETE RESTRICT,
    executed_by_username    text,
    started_at              timestamptz,
    completed_at            timestamptz,

    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_execution_status_check CHECK (
        status IN (
            'PREPARED',
            'PREFLIGHT_PASSED',
            'PREFLIGHT_BLOCKED',
            'APPROVED',
            'EXECUTING',
            'SUCCEEDED',
            'FAILED',
            'PARTIAL_FAILED',
            'CANCELLED'
        )
    ),
    CONSTRAINT recovery_execution_idempotency_check CHECK (length(trim(idempotency_key)) > 0),
    CONSTRAINT recovery_execution_hashes_check CHECK (
        length(publication_hash)=64 AND length(run_result_hash)=64 AND length(action_set_hash)=64
        AND (preflight_hash IS NULL OR length(preflight_hash)=64)
        AND (approval_hash IS NULL OR length(approval_hash)=64)
        AND (execution_result_hash IS NULL OR length(execution_result_hash)=64)
    ),
    CONSTRAINT recovery_execution_counts_check CHECK (
        action_count >= 0 AND succeeded_action_count >= 0 AND failed_action_count >= 0
        AND succeeded_action_count + failed_action_count <= action_count
    ),
    CONSTRAINT recovery_execution_approval_check CHECK (
        (approved_by_user_id IS NULL AND approved_by_username IS NULL AND approved_at IS NULL AND approval_hash IS NULL)
        OR
        (approved_by_user_id IS NOT NULL AND approved_by_username IS NOT NULL AND approved_at IS NOT NULL AND approval_hash IS NOT NULL)
    ),
    CONSTRAINT recovery_execution_separation_check CHECK (
        approved_by_user_id IS NULL OR approved_by_user_id <> prepared_by_user_id
    ),
    CONSTRAINT recovery_execution_executor_check CHECK (
        (executed_by_user_id IS NULL AND executed_by_username IS NULL AND started_at IS NULL)
        OR
        (executed_by_user_id IS NOT NULL AND executed_by_username IS NOT NULL AND started_at IS NOT NULL)
    ),
    CONSTRAINT recovery_execution_terminal_check CHECK (
        status NOT IN ('SUCCEEDED','FAILED','PARTIAL_FAILED','CANCELLED') OR completed_at IS NOT NULL
    ),
    CONSTRAINT recovery_execution_terminal_result_hash_check CHECK (
        status NOT IN ('SUCCEEDED','FAILED','PARTIAL_FAILED') OR execution_result_hash IS NOT NULL
    ),
    CONSTRAINT recovery_execution_nonterminal_result_hash_check CHECK (
        execution_result_hash IS NULL OR status IN ('SUCCEEDED','FAILED','PARTIAL_FAILED')
    ),
    CONSTRAINT recovery_execution_preflight_check CHECK (
        status NOT IN ('PREFLIGHT_PASSED','APPROVED','EXECUTING','SUCCEEDED','PARTIAL_FAILED')
        OR (preflight_hash IS NOT NULL AND preflighted_at IS NOT NULL)
    ),
    CONSTRAINT recovery_execution_approved_state_check CHECK (
        status NOT IN ('APPROVED','EXECUTING','SUCCEEDED','PARTIAL_FAILED')
        OR approved_by_user_id IS NOT NULL
    ),
    CONSTRAINT recovery_execution_idempotency_unique UNIQUE (scenario_id, idempotency_key)
);

CREATE INDEX recovery_plan_executions_scenario_idx
    ON recovery_plan_executions(scenario_id, prepared_at DESC, id DESC);
CREATE INDEX recovery_plan_executions_status_idx
    ON recovery_plan_executions(status, updated_at DESC, id DESC);

CREATE TABLE recovery_plan_execution_actions (
    id                      uuid PRIMARY KEY,
    execution_id            uuid NOT NULL REFERENCES recovery_plan_executions(id) ON DELETE RESTRICT,
    scenario_action_id      uuid NOT NULL REFERENCES recovery_scenario_actions(id) ON DELETE RESTRICT,
    sequence_no             integer NOT NULL,

    action_type             text NOT NULL,
    target_type             text NOT NULL,
    target_ref              text NOT NULL,
    parameters              jsonb NOT NULL DEFAULT '{}'::jsonb,
    estimated_cost          numeric(18,2) NOT NULL DEFAULT 0,
    note                    text NOT NULL DEFAULT '',
    action_snapshot_hash    text NOT NULL,

    preflight_status        text NOT NULL DEFAULT 'PENDING',
    execution_status        text NOT NULL DEFAULT 'PENDING',
    preflight_state         jsonb NOT NULL DEFAULT '{}'::jsonb,
    preflight_state_hash    text,

    before_state            jsonb NOT NULL DEFAULT '{}'::jsonb,
    after_state             jsonb NOT NULL DEFAULT '{}'::jsonb,
    operational_ref         text NOT NULL DEFAULT '',
    result_hash             text,
    error_text              text NOT NULL DEFAULT '',

    started_at              timestamptz,
    completed_at            timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_execution_action_sequence_check CHECK (sequence_no > 0),
    CONSTRAINT recovery_execution_action_type_check CHECK (
        action_type IN ('EXPEDITE_PO','ALTERNATE_WORK_CENTER','ADD_OVERTIME_CAPACITY','RESCHEDULE_WO','RELEASE_WO')
    ),
    CONSTRAINT recovery_execution_action_target_type_check CHECK (
        target_type IN ('PURCHASE_ORDER','WORK_ORDER','WORK_ORDER_OPERATION','WORK_CENTER')
    ),
    CONSTRAINT recovery_execution_action_target_ref_check CHECK (length(trim(target_ref)) > 0),
    CONSTRAINT recovery_execution_action_parameters_check CHECK (jsonb_typeof(parameters)='object'),
    CONSTRAINT recovery_execution_action_cost_check CHECK (estimated_cost >= 0),
    CONSTRAINT recovery_execution_action_snapshot_hash_check CHECK (length(action_snapshot_hash)=64),
    CONSTRAINT recovery_execution_action_preflight_status_check CHECK (
        preflight_status IN ('PENDING','READY','BLOCKED')
    ),
    CONSTRAINT recovery_execution_action_status_check CHECK (
        execution_status IN ('PENDING','RUNNING','SUCCEEDED','FAILED','SKIPPED')
    ),
    CONSTRAINT recovery_execution_action_preflight_state_check CHECK (jsonb_typeof(preflight_state)='object'),
    CONSTRAINT recovery_execution_action_before_state_check CHECK (jsonb_typeof(before_state)='object'),
    CONSTRAINT recovery_execution_action_after_state_check CHECK (jsonb_typeof(after_state)='object'),
    CONSTRAINT recovery_execution_action_hash_check CHECK (
        (preflight_state_hash IS NULL OR length(preflight_state_hash)=64)
        AND (result_hash IS NULL OR length(result_hash)=64)
    ),
    CONSTRAINT recovery_execution_action_unique_sequence UNIQUE(execution_id, sequence_no),
    CONSTRAINT recovery_execution_action_unique_source UNIQUE(execution_id, scenario_action_id)
);

CREATE INDEX recovery_plan_execution_actions_idx
    ON recovery_plan_execution_actions(execution_id, sequence_no, id);

-- Temporary, date-scoped overtime created by an executed recovery action.
-- It expires naturally after effective_date; master Work Center capacity is not modified.
CREATE TABLE recovery_capacity_overrides (
    id                  uuid PRIMARY KEY,
    execution_action_id uuid NOT NULL UNIQUE REFERENCES recovery_plan_execution_actions(id) ON DELETE RESTRICT,
    execution_id        uuid NOT NULL REFERENCES recovery_plan_executions(id) ON DELETE RESTRICT,
    work_center_id      uuid NOT NULL REFERENCES work_centers(id) ON DELETE RESTRICT,
    effective_date      date NOT NULL,
    overtime_minutes    integer NOT NULL,
    created_by_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_by_username text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recovery_capacity_override_minutes_check CHECK (overtime_minutes BETWEEN 1 AND 10080)
);

CREATE INDEX recovery_capacity_overrides_wc_date_idx
    ON recovery_capacity_overrides(work_center_id, effective_date);

-- ------------------------------------------------------------
-- Actor and lifecycle guards
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION validate_recovery_execution_actor(
    p_user uuid,
    p_name text,
    p_admin_only boolean DEFAULT false
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    urole text;
    uname text;
    active boolean;
BEGIN
    SELECT role, username, is_active
      INTO urole, uname, active
      FROM users
     WHERE id=p_user;

    IF urole IS NULL OR NOT active OR uname IS DISTINCT FROM p_name THEN
        RAISE EXCEPTION 'recovery execution requires matching active actor' USING ERRCODE='42501';
    END IF;

    IF p_admin_only AND urole <> 'admin' THEN
        RAISE EXCEPTION 'recovery execution approval/execution requires admin actor' USING ERRCODE='42501';
    END IF;

    IF NOT p_admin_only AND urole NOT IN ('planner','admin') THEN
        RAISE EXCEPTION 'recovery execution preparation requires planner/admin actor' USING ERRCODE='42501';
    END IF;
END$$;

CREATE OR REPLACE FUNCTION guard_recovery_plan_execution()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        PERFORM validate_recovery_execution_actor(NEW.prepared_by_user_id, NEW.prepared_by_username, false);
        IF NEW.status <> 'PREPARED' THEN
            RAISE EXCEPTION 'recovery execution must start PREPARED' USING ERRCODE='23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.scenario_id IS DISTINCT FROM OLD.scenario_id
       OR NEW.publication_id IS DISTINCT FROM OLD.publication_id
       OR NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.publication_hash IS DISTINCT FROM OLD.publication_hash
       OR NEW.run_result_hash IS DISTINCT FROM OLD.run_result_hash
       OR NEW.action_set_hash IS DISTINCT FROM OLD.action_set_hash
       OR NEW.prepare_comment IS DISTINCT FROM OLD.prepare_comment
       OR NEW.action_count IS DISTINCT FROM OLD.action_count
       OR NEW.prepared_by_user_id IS DISTINCT FROM OLD.prepared_by_user_id
       OR NEW.prepared_by_username IS DISTINCT FROM OLD.prepared_by_username
       OR NEW.prepared_at IS DISTINCT FROM OLD.prepared_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'recovery execution identity/snapshot fields are immutable' USING ERRCODE='23514';
    END IF;

    IF OLD.status IN ('SUCCEEDED','FAILED','PARTIAL_FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'terminal recovery execution is immutable' USING ERRCODE='23514';
    END IF;

    IF OLD.status='PREPARED' AND NEW.status NOT IN ('PREPARED','PREFLIGHT_PASSED','PREFLIGHT_BLOCKED','CANCELLED') THEN
        RAISE EXCEPTION 'invalid recovery execution transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    ELSIF OLD.status='PREFLIGHT_BLOCKED' AND NEW.status NOT IN ('PREFLIGHT_BLOCKED','PREFLIGHT_PASSED','CANCELLED') THEN
        RAISE EXCEPTION 'invalid recovery execution transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    ELSIF OLD.status='PREFLIGHT_PASSED' AND NEW.status NOT IN ('PREFLIGHT_PASSED','PREFLIGHT_BLOCKED','APPROVED','CANCELLED') THEN
        RAISE EXCEPTION 'invalid recovery execution transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    ELSIF OLD.status='APPROVED' AND NEW.status NOT IN ('APPROVED','EXECUTING','FAILED','CANCELLED') THEN
        RAISE EXCEPTION 'invalid recovery execution transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    ELSIF OLD.status='EXECUTING' AND NEW.status NOT IN ('EXECUTING','SUCCEEDED','FAILED','PARTIAL_FAILED') THEN
        RAISE EXCEPTION 'invalid recovery execution transition % -> %', OLD.status, NEW.status USING ERRCODE='23514';
    END IF;

    IF NEW.status='APPROVED' AND OLD.status<>'APPROVED' THEN
        PERFORM validate_recovery_execution_actor(NEW.approved_by_user_id, NEW.approved_by_username, true);
        IF NEW.approved_by_user_id = NEW.prepared_by_user_id THEN
            RAISE EXCEPTION 'recovery execution approver must differ from preparer' USING ERRCODE='42501';
        END IF;
    END IF;

    IF NEW.status='EXECUTING' AND OLD.status<>'EXECUTING' THEN
        PERFORM validate_recovery_execution_actor(NEW.executed_by_user_id, NEW.executed_by_username, true);
    END IF;

    NEW.updated_at := now();
    RETURN NEW;
END$$;

CREATE TRIGGER recovery_plan_execution_guard_trg
BEFORE INSERT OR UPDATE ON recovery_plan_executions
FOR EACH ROW EXECUTE FUNCTION guard_recovery_plan_execution();

CREATE OR REPLACE FUNCTION guard_recovery_plan_execution_action()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.execution_id IS DISTINCT FROM OLD.execution_id
       OR NEW.scenario_action_id IS DISTINCT FROM OLD.scenario_action_id
       OR NEW.sequence_no IS DISTINCT FROM OLD.sequence_no
       OR NEW.action_type IS DISTINCT FROM OLD.action_type
       OR NEW.target_type IS DISTINCT FROM OLD.target_type
       OR NEW.target_ref IS DISTINCT FROM OLD.target_ref
       OR NEW.parameters IS DISTINCT FROM OLD.parameters
       OR NEW.estimated_cost IS DISTINCT FROM OLD.estimated_cost
       OR NEW.note IS DISTINCT FROM OLD.note
       OR NEW.action_snapshot_hash IS DISTINCT FROM OLD.action_snapshot_hash
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'recovery execution action snapshot is immutable' USING ERRCODE='23514';
    END IF;

    IF OLD.execution_status IN ('SUCCEEDED','FAILED','SKIPPED') THEN
        RAISE EXCEPTION 'terminal recovery execution action is immutable' USING ERRCODE='23514';
    END IF;

    NEW.updated_at := now();
    RETURN NEW;
END$$;

CREATE TRIGGER recovery_plan_execution_action_guard_trg
BEFORE UPDATE ON recovery_plan_execution_actions
FOR EACH ROW EXECUTE FUNCTION guard_recovery_plan_execution_action();

CREATE OR REPLACE FUNCTION reject_recovery_execution_evidence_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'recovery execution evidence is append-only' USING ERRCODE='23514';
END$$;

CREATE TRIGGER recovery_plan_executions_no_delete_trg
BEFORE DELETE ON recovery_plan_executions
FOR EACH ROW EXECUTE FUNCTION reject_recovery_execution_evidence_delete();
CREATE TRIGGER recovery_plan_execution_actions_no_delete_trg
BEFORE DELETE ON recovery_plan_execution_actions
FOR EACH ROW EXECUTE FUNCTION reject_recovery_execution_evidence_delete();
CREATE TRIGGER recovery_capacity_overrides_immutable_trg
BEFORE UPDATE OR DELETE ON recovery_capacity_overrides
FOR EACH ROW EXECUTE FUNCTION reject_recovery_execution_evidence_delete();
