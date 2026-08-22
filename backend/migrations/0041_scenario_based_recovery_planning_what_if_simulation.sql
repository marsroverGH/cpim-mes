-- 0041 Scenario-Based Recovery Planning / What-if Simulation
--
-- A Recovery Scenario is a side-effect-free hypothetical intervention plan.
--
-- Important:
--   Simulation never directly mutates operational:
--     * purchase_orders
--     * work_orders
--     * work_order_operations
--     * work_centers
--     * sales_orders
--
-- Published scenarios are approved recovery-plan evidence.
-- Operational execution remains a separate explicit step.

CREATE TABLE recovery_scenarios (
    id                  uuid PRIMARY KEY,
    scenario_no         text NOT NULL UNIQUE,
    name                text NOT NULL,
    description         text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'DRAFT',

    baseline_as_of      timestamptz NOT NULL,

    created_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
    created_by_username text NOT NULL,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    published_at        timestamptz,

    CONSTRAINT recovery_scenarios_status_check
        CHECK (
            status IN (
                'DRAFT',
                'SIMULATED',
                'PUBLISHED',
                'ARCHIVED'
            )
        ),

    CONSTRAINT recovery_scenarios_no_check
        CHECK (length(trim(scenario_no)) > 0),

    CONSTRAINT recovery_scenarios_name_check
        CHECK (length(trim(name)) > 0),

    CONSTRAINT recovery_scenarios_publication_check
        CHECK (
            status <> 'PUBLISHED'
            OR published_at IS NOT NULL
        )
);

CREATE INDEX recovery_scenarios_status_idx
    ON recovery_scenarios(
        status,
        updated_at DESC,
        id DESC
    );


CREATE TABLE recovery_scenario_actions (
    id              uuid PRIMARY KEY,

    scenario_id     uuid NOT NULL
                        REFERENCES recovery_scenarios(id)
                        ON DELETE RESTRICT,

    sequence_no     integer NOT NULL,

    action_type     text NOT NULL,
    target_type     text NOT NULL,
    target_ref      text NOT NULL,

    parameters      jsonb NOT NULL DEFAULT '{}'::jsonb,

    estimated_cost  numeric(18,2) NOT NULL DEFAULT 0,
    note            text NOT NULL DEFAULT '',

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_scenario_actions_sequence_check
        CHECK (sequence_no > 0),

    CONSTRAINT recovery_scenario_actions_type_check
        CHECK (
            action_type IN (
                'EXPEDITE_PO',
                'ALTERNATE_WORK_CENTER',
                'ADD_OVERTIME_CAPACITY',
                'RESCHEDULE_WO',
                'RELEASE_WO'
            )
        ),

    CONSTRAINT recovery_scenario_actions_target_type_check
        CHECK (
            target_type IN (
                'PURCHASE_ORDER',
                'WORK_ORDER',
                'WORK_ORDER_OPERATION',
                'WORK_CENTER'
            )
        ),

    CONSTRAINT recovery_scenario_actions_target_ref_check
        CHECK (length(trim(target_ref)) > 0),

    CONSTRAINT recovery_scenario_actions_parameters_check
        CHECK (jsonb_typeof(parameters) = 'object'),

    CONSTRAINT recovery_scenario_actions_cost_check
        CHECK (estimated_cost >= 0),

    CONSTRAINT recovery_scenario_actions_sequence_unique
        UNIQUE (
            scenario_id,
            sequence_no
        )
);

CREATE INDEX recovery_scenario_actions_scenario_idx
    ON recovery_scenario_actions(
        scenario_id,
        sequence_no,
        id
    );

CREATE INDEX recovery_scenario_actions_target_idx
    ON recovery_scenario_actions(
        target_type,
        target_ref
    );


CREATE TABLE recovery_scenario_runs (
    id                  uuid PRIMARY KEY,

    scenario_id         uuid NOT NULL
                            REFERENCES recovery_scenarios(id)
                            ON DELETE RESTRICT,

    status              text NOT NULL,

    baseline_as_of      timestamptz NOT NULL,
    horizon_days        integer NOT NULL,

    baseline_hash       text NOT NULL,
    request_hash        text NOT NULL,
    result_hash         text,

    created_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
    created_by_username text NOT NULL,

    started_at          timestamptz NOT NULL DEFAULT now(),
    completed_at        timestamptz,

    failure_reason      text NOT NULL DEFAULT '',

    CONSTRAINT recovery_scenario_runs_status_check
        CHECK (
            status IN (
                'RUNNING',
                'SUCCEEDED',
                'FAILED'
            )
        ),

    CONSTRAINT recovery_scenario_runs_horizon_check
        CHECK (
            horizon_days BETWEEN 1 AND 730
        ),

    CONSTRAINT recovery_scenario_runs_baseline_hash_check
        CHECK (length(baseline_hash) = 64),

    CONSTRAINT recovery_scenario_runs_request_hash_check
        CHECK (length(request_hash) = 64),

    CONSTRAINT recovery_scenario_runs_result_hash_check
        CHECK (
            result_hash IS NULL
            OR length(result_hash) = 64
        ),

    CONSTRAINT recovery_scenario_runs_terminal_check
        CHECK (
            (
                status = 'RUNNING'
                AND completed_at IS NULL
                AND result_hash IS NULL
            )
            OR
            (
                status = 'SUCCEEDED'
                AND completed_at IS NOT NULL
                AND result_hash IS NOT NULL
            )
            OR
            (
                status = 'FAILED'
                AND completed_at IS NOT NULL
            )
        )
);

CREATE UNIQUE INDEX recovery_scenario_runs_success_request_uidx
    ON recovery_scenario_runs(
        scenario_id,
        request_hash
    )
    WHERE status = 'SUCCEEDED';

CREATE INDEX recovery_scenario_runs_scenario_idx
    ON recovery_scenario_runs(
        scenario_id,
        started_at DESC,
        id DESC
    );


CREATE TABLE recovery_scenario_case_results (
    id                          uuid PRIMARY KEY,

    run_id                      uuid NOT NULL
                                    REFERENCES recovery_scenario_runs(id)
                                    ON DELETE RESTRICT,

    case_id                     uuid NOT NULL
                                    REFERENCES control_tower_cases(id)
                                    ON DELETE RESTRICT,

    baseline_priority_band      text NOT NULL,
    baseline_priority_score     numeric(8,3) NOT NULL,
    baseline_revenue_at_risk    numeric(18,2) NOT NULL,
    baseline_impact_days        integer NOT NULL,

    simulated_resolved          boolean NOT NULL DEFAULT false,

    simulated_priority_band     text NOT NULL,
    simulated_priority_score    numeric(8,3) NOT NULL,
    simulated_revenue_at_risk   numeric(18,2) NOT NULL,
    simulated_impact_days       integer NOT NULL,

    recovery_days               integer NOT NULL DEFAULT 0,
    revenue_recovered           numeric(18,2) NOT NULL DEFAULT 0,

    matched_action_ids          jsonb NOT NULL DEFAULT '[]'::jsonb,
    explanation                 jsonb NOT NULL DEFAULT '{}'::jsonb,

    result_hash                 text NOT NULL,

    created_at                  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_case_baseline_band_check
        CHECK (
            baseline_priority_band IN (
                'P1',
                'P2',
                'P3',
                'P4'
            )
        ),

    CONSTRAINT recovery_case_simulated_band_check
        CHECK (
            simulated_priority_band IN (
                'P1',
                'P2',
                'P3',
                'P4'
            )
        ),

    CONSTRAINT recovery_case_baseline_score_check
        CHECK (
            baseline_priority_score >= 0
            AND baseline_priority_score <= 100
        ),

    CONSTRAINT recovery_case_simulated_score_check
        CHECK (
            simulated_priority_score >= 0
            AND simulated_priority_score <= 100
        ),

    CONSTRAINT recovery_case_revenue_check
        CHECK (
            baseline_revenue_at_risk >= 0
            AND simulated_revenue_at_risk >= 0
            AND revenue_recovered >= 0
        ),

    CONSTRAINT recovery_case_impact_check
        CHECK (
            baseline_impact_days >= 0
            AND simulated_impact_days >= 0
            AND recovery_days >= 0
        ),

    CONSTRAINT recovery_case_action_ids_check
        CHECK (
            jsonb_typeof(matched_action_ids) = 'array'
        ),

    CONSTRAINT recovery_case_explanation_check
        CHECK (
            jsonb_typeof(explanation) = 'object'
        ),

    CONSTRAINT recovery_case_hash_check
        CHECK (
            length(result_hash) = 64
        ),

    CONSTRAINT recovery_case_unique
        UNIQUE (
            run_id,
            case_id
        )
);

CREATE INDEX recovery_scenario_case_results_run_idx
    ON recovery_scenario_case_results(
        run_id,
        simulated_priority_band,
        simulated_priority_score DESC,
        id
    );


CREATE TABLE recovery_scenario_action_results (
    id                      uuid PRIMARY KEY,

    run_id                  uuid NOT NULL
                                REFERENCES recovery_scenario_runs(id)
                                ON DELETE RESTRICT,

    action_id               uuid NOT NULL
                                REFERENCES recovery_scenario_actions(id)
                                ON DELETE RESTRICT,

    affected_cases          integer NOT NULL DEFAULT 0,
    impact_days_recovered   integer NOT NULL DEFAULT 0,
    revenue_recovered       numeric(18,2) NOT NULL DEFAULT 0,
    estimated_cost          numeric(18,2) NOT NULL DEFAULT 0,

    evidence                jsonb NOT NULL DEFAULT '{}'::jsonb,

    result_hash             text NOT NULL,

    created_at              timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_action_result_count_check
        CHECK (
            affected_cases >= 0
            AND impact_days_recovered >= 0
        ),

    CONSTRAINT recovery_action_result_value_check
        CHECK (
            revenue_recovered >= 0
            AND estimated_cost >= 0
        ),

    CONSTRAINT recovery_action_result_evidence_check
        CHECK (
            jsonb_typeof(evidence) = 'object'
        ),

    CONSTRAINT recovery_action_result_hash_check
        CHECK (
            length(result_hash) = 64
        ),

    CONSTRAINT recovery_action_result_unique
        UNIQUE (
            run_id,
            action_id
        )
);

CREATE INDEX recovery_scenario_action_results_run_idx
    ON recovery_scenario_action_results(
        run_id,
        action_id
    );


CREATE TABLE recovery_scenario_summaries (
    id                          uuid PRIMARY KEY,

    run_id                      uuid NOT NULL UNIQUE
                                    REFERENCES recovery_scenario_runs(id)
                                    ON DELETE RESTRICT,

    baseline_open_cases         integer NOT NULL,
    simulated_open_cases        integer NOT NULL,

    baseline_p1_cases           integer NOT NULL,
    simulated_p1_cases          integer NOT NULL,

    baseline_p2_cases           integer NOT NULL,
    simulated_p2_cases          integer NOT NULL,

    baseline_revenue_at_risk    numeric(18,2) NOT NULL,
    simulated_revenue_at_risk   numeric(18,2) NOT NULL,

    baseline_impact_days        integer NOT NULL,
    simulated_impact_days       integer NOT NULL,

    recovered_revenue           numeric(18,2) NOT NULL,
    p1_reduction                integer NOT NULL,
    open_case_reduction         integer NOT NULL,
    impact_days_recovered       integer NOT NULL,

    estimated_action_cost       numeric(18,2) NOT NULL,
    net_value                   numeric(18,2) NOT NULL,

    recovery_score              numeric(8,3) NOT NULL,

    result_hash                 text NOT NULL,

    created_at                  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_summary_case_count_check
        CHECK (
            baseline_open_cases >= 0
            AND simulated_open_cases >= 0
            AND baseline_p1_cases >= 0
            AND simulated_p1_cases >= 0
            AND baseline_p2_cases >= 0
            AND simulated_p2_cases >= 0
        ),

    CONSTRAINT recovery_summary_revenue_check
        CHECK (
            baseline_revenue_at_risk >= 0
            AND simulated_revenue_at_risk >= 0
            AND recovered_revenue >= 0
            AND estimated_action_cost >= 0
        ),

    CONSTRAINT recovery_summary_impact_check
        CHECK (
            baseline_impact_days >= 0
            AND simulated_impact_days >= 0
            AND impact_days_recovered >= 0
        ),

    CONSTRAINT recovery_summary_score_check
        CHECK (
            recovery_score >= 0
            AND recovery_score <= 100
        ),

    CONSTRAINT recovery_summary_hash_check
        CHECK (
            length(result_hash) = 64
        )
);

CREATE INDEX recovery_scenario_summary_score_idx
    ON recovery_scenario_summaries(
        recovery_score DESC,
        recovered_revenue DESC,
        id
    );


CREATE TABLE recovery_scenario_publications (
    id                      uuid PRIMARY KEY,

    scenario_id             uuid NOT NULL UNIQUE
                                REFERENCES recovery_scenarios(id)
                                ON DELETE RESTRICT,

    run_id                  uuid NOT NULL UNIQUE
                                REFERENCES recovery_scenario_runs(id)
                                ON DELETE RESTRICT,

    publication_hash        text NOT NULL,

    comment                 text NOT NULL DEFAULT '',

    published_by_user_id    uuid REFERENCES users(id) ON DELETE RESTRICT,
    published_by_username   text NOT NULL,

    published_at            timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recovery_publication_hash_check
        CHECK (
            length(publication_hash) = 64
        )
);

CREATE INDEX recovery_scenario_publications_time_idx
    ON recovery_scenario_publications(
        published_at DESC,
        id DESC
    );


-- ------------------------------------------------------------
-- Scenario lifecycle
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION guard_recovery_scenario_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.scenario_no <> OLD.scenario_no
       OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id
       OR NEW.created_by_username <> OLD.created_by_username
       OR NEW.created_at <> OLD.created_at
    THEN
        RAISE EXCEPTION
            'recovery scenario identity/audit fields are immutable'
            USING ERRCODE='23514';
    END IF;

    IF OLD.status <> 'DRAFT'
       AND (
           NEW.name <> OLD.name
           OR NEW.description <> OLD.description
           OR NEW.baseline_as_of <> OLD.baseline_as_of
       )
    THEN
        RAISE EXCEPTION
            'simulated/published recovery scenario definition is immutable'
            USING ERRCODE='23514';
    END IF;

    IF OLD.status = 'DRAFT'
       AND NEW.status NOT IN (
           'DRAFT',
           'SIMULATED',
           'ARCHIVED'
       )
    THEN
        RAISE EXCEPTION
            'invalid recovery scenario transition % -> %',
            OLD.status,
            NEW.status
            USING ERRCODE='23514';
    END IF;

    IF OLD.status = 'SIMULATED'
       AND NEW.status NOT IN (
           'SIMULATED',
           'PUBLISHED',
           'ARCHIVED'
       )
    THEN
        RAISE EXCEPTION
            'invalid recovery scenario transition % -> %',
            OLD.status,
            NEW.status
            USING ERRCODE='23514';
    END IF;

    IF OLD.status = 'PUBLISHED'
       AND NEW.status NOT IN (
           'PUBLISHED',
           'ARCHIVED'
       )
    THEN
        RAISE EXCEPTION
            'invalid recovery scenario transition % -> %',
            OLD.status,
            NEW.status
            USING ERRCODE='23514';
    END IF;

    IF OLD.status = 'ARCHIVED'
       AND NEW.status <> 'ARCHIVED'
    THEN
        RAISE EXCEPTION
            'ARCHIVED recovery scenario is terminal'
            USING ERRCODE='23514';
    END IF;

    IF NEW.status = 'PUBLISHED'
       AND NEW.published_at IS NULL
    THEN
        RAISE EXCEPTION
            'published recovery scenario requires published_at'
            USING ERRCODE='23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_scenario_update_guard_trg
BEFORE UPDATE
ON recovery_scenarios
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_scenario_update();


-- ------------------------------------------------------------
-- Actions are editable only while scenario is DRAFT.
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION guard_recovery_scenario_action()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scenario_status text;
    sid uuid;
BEGIN
    sid := CASE
        WHEN TG_OP = 'DELETE'
            THEN OLD.scenario_id
        ELSE NEW.scenario_id
    END;

    SELECT status
      INTO scenario_status
      FROM recovery_scenarios
     WHERE id = sid
     FOR UPDATE;

    IF scenario_status IS NULL THEN
        RAISE EXCEPTION
            'recovery scenario does not exist'
            USING ERRCODE='23503';
    END IF;

    IF scenario_status <> 'DRAFT' THEN
        RAISE EXCEPTION
            'recovery scenario actions are editable only while DRAFT'
            USING ERRCODE='23514';
    END IF;

    IF TG_OP = 'UPDATE'
       AND (
           NEW.id <> OLD.id
           OR NEW.scenario_id <> OLD.scenario_id
           OR NEW.created_at <> OLD.created_at
       )
    THEN
        RAISE EXCEPTION
            'recovery scenario action identity is immutable'
            USING ERRCODE='23514';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_scenario_action_guard_trg
BEFORE INSERT OR UPDATE OR DELETE
ON recovery_scenario_actions
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_scenario_action();


-- ------------------------------------------------------------
-- Run lifecycle
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION guard_recovery_scenario_run()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scenario_status text;
    action_count integer;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION
            'recovery scenario runs are append-only'
            USING ERRCODE='23514';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT status
          INTO scenario_status
          FROM recovery_scenarios
         WHERE id = NEW.scenario_id
         FOR UPDATE;

        IF scenario_status NOT IN (
            'DRAFT',
            'SIMULATED'
        ) THEN
            RAISE EXCEPTION
                'recovery scenario cannot be simulated in status %',
                scenario_status
                USING ERRCODE='23514';
        END IF;

        SELECT count(*)
          INTO action_count
          FROM recovery_scenario_actions
         WHERE scenario_id = NEW.scenario_id;

        IF action_count = 0 THEN
            RAISE EXCEPTION
                'recovery scenario requires at least one action'
                USING ERRCODE='23514';
        END IF;

        IF NEW.status <> 'RUNNING' THEN
            RAISE EXCEPTION
                'recovery scenario run must start RUNNING'
                USING ERRCODE='23514';
        END IF;

        RETURN NEW;
    END IF;

    IF OLD.status <> 'RUNNING' THEN
        RAISE EXCEPTION
            'completed recovery scenario run is immutable'
            USING ERRCODE='23514';
    END IF;

    IF NEW.id <> OLD.id
       OR NEW.scenario_id <> OLD.scenario_id
       OR NEW.baseline_as_of <> OLD.baseline_as_of
       OR NEW.horizon_days <> OLD.horizon_days
       OR NEW.baseline_hash <> OLD.baseline_hash
       OR NEW.request_hash <> OLD.request_hash
       OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id
       OR NEW.created_by_username <> OLD.created_by_username
       OR NEW.started_at <> OLD.started_at
    THEN
        RAISE EXCEPTION
            'recovery scenario run request/audit fields are immutable'
            USING ERRCODE='23514';
    END IF;

    IF NEW.status NOT IN (
        'SUCCEEDED',
        'FAILED'
    ) THEN
        RAISE EXCEPTION
            'recovery scenario run may only transition RUNNING -> SUCCEEDED/FAILED'
            USING ERRCODE='23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_scenario_run_guard_trg
BEFORE INSERT OR UPDATE OR DELETE
ON recovery_scenario_runs
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_scenario_run();


-- ------------------------------------------------------------
-- Simulation evidence can only be inserted while run is RUNNING.
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION guard_recovery_simulation_result_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    run_status text;
BEGIN
    SELECT status
      INTO run_status
      FROM recovery_scenario_runs
     WHERE id = NEW.run_id;

    IF run_status <> 'RUNNING' THEN
        RAISE EXCEPTION
            'recovery simulation result may only be inserted while run is RUNNING'
            USING ERRCODE='23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_case_result_insert_guard_trg
BEFORE INSERT
ON recovery_scenario_case_results
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_simulation_result_insert();

CREATE TRIGGER recovery_action_result_insert_guard_trg
BEFORE INSERT
ON recovery_scenario_action_results
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_simulation_result_insert();

CREATE TRIGGER recovery_summary_insert_guard_trg
BEFORE INSERT
ON recovery_scenario_summaries
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_simulation_result_insert();


-- ------------------------------------------------------------
-- Successful simulation evidence is immutable.
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION reject_recovery_simulation_evidence_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION
        '% is immutable recovery simulation evidence',
        TG_TABLE_NAME
        USING ERRCODE='23514';
END;
$$;

CREATE TRIGGER recovery_case_results_immutable_trg
BEFORE UPDATE OR DELETE
ON recovery_scenario_case_results
FOR EACH ROW
EXECUTE FUNCTION reject_recovery_simulation_evidence_mutation();

CREATE TRIGGER recovery_action_results_immutable_trg
BEFORE UPDATE OR DELETE
ON recovery_scenario_action_results
FOR EACH ROW
EXECUTE FUNCTION reject_recovery_simulation_evidence_mutation();

CREATE TRIGGER recovery_summaries_immutable_trg
BEFORE UPDATE OR DELETE
ON recovery_scenario_summaries
FOR EACH ROW
EXECUTE FUNCTION reject_recovery_simulation_evidence_mutation();


-- ------------------------------------------------------------
-- Publication approves a plan.
-- It does NOT execute the hypothetical actions.
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION guard_recovery_scenario_publication()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    scenario_status text;
    run_status text;
    run_scenario_id uuid;
    summary_count integer;
BEGIN
    SELECT status
      INTO scenario_status
      FROM recovery_scenarios
     WHERE id = NEW.scenario_id
     FOR UPDATE;

    IF scenario_status <> 'SIMULATED' THEN
        RAISE EXCEPTION
            'only SIMULATED recovery scenario may be published'
            USING ERRCODE='23514';
    END IF;

    SELECT status, scenario_id
      INTO run_status, run_scenario_id
      FROM recovery_scenario_runs
     WHERE id = NEW.run_id;

    IF run_status <> 'SUCCEEDED'
       OR run_scenario_id <> NEW.scenario_id
    THEN
        RAISE EXCEPTION
            'publication requires successful run for same recovery scenario'
            USING ERRCODE='23514';
    END IF;

    SELECT count(*)
      INTO summary_count
      FROM recovery_scenario_summaries
     WHERE run_id = NEW.run_id;

    IF summary_count <> 1 THEN
        RAISE EXCEPTION
            'publication requires exactly one recovery scenario summary'
            USING ERRCODE='23514';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_publication_guard_trg
BEFORE INSERT
ON recovery_scenario_publications
FOR EACH ROW
EXECUTE FUNCTION guard_recovery_scenario_publication();

CREATE TRIGGER recovery_publications_immutable_trg
BEFORE UPDATE OR DELETE
ON recovery_scenario_publications
FOR EACH ROW
EXECUTE FUNCTION reject_recovery_simulation_evidence_mutation();


-- ------------------------------------------------------------
-- Latest successful run per scenario
-- ------------------------------------------------------------

CREATE VIEW v_latest_recovery_scenario_runs AS
WITH latest AS (
    SELECT DISTINCT ON (scenario_id)
        id
    FROM recovery_scenario_runs
    WHERE status = 'SUCCEEDED'
    ORDER BY
        scenario_id,
        completed_at DESC,
        id DESC
)
SELECT
    s.id AS scenario_id,
    s.scenario_no,
    s.name,
    s.description,

    s.status AS scenario_status,

    s.baseline_as_of AS scenario_baseline_as_of,

    s.created_by_user_id,
    s.created_by_username,

    s.created_at AS scenario_created_at,
    s.updated_at AS scenario_updated_at,
    s.published_at,

    r.id AS run_id,
    r.baseline_as_of,
    r.horizon_days,
    r.baseline_hash,
    r.request_hash,
    r.result_hash,
    r.started_at,
    r.completed_at,

    sm.baseline_open_cases,
    sm.simulated_open_cases,

    sm.baseline_p1_cases,
    sm.simulated_p1_cases,

    sm.baseline_p2_cases,
    sm.simulated_p2_cases,

    sm.baseline_revenue_at_risk,
    sm.simulated_revenue_at_risk,

    sm.baseline_impact_days,
    sm.simulated_impact_days,

    sm.recovered_revenue,
    sm.p1_reduction,
    sm.open_case_reduction,
    sm.impact_days_recovered,

    sm.estimated_action_cost,
    sm.net_value,
    sm.recovery_score,

    (p.id IS NOT NULL) AS is_published,
    p.id AS publication_id

FROM latest l

JOIN recovery_scenario_runs r
  ON r.id = l.id

JOIN recovery_scenarios s
  ON s.id = r.scenario_id

JOIN recovery_scenario_summaries sm
  ON sm.run_id = r.id

LEFT JOIN recovery_scenario_publications p
  ON p.scenario_id = s.id
 AND p.run_id = r.id;


-- ------------------------------------------------------------
-- Comparable scenarios must share the same baseline_hash.
-- ------------------------------------------------------------

CREATE VIEW v_recovery_scenario_comparison AS
SELECT
    x.*,

    row_number() OVER (
        PARTITION BY x.baseline_hash
        ORDER BY
            x.p1_reduction DESC,
            x.recovered_revenue DESC,
            x.impact_days_recovered DESC,
            x.net_value DESC,
            x.recovery_score DESC,
            x.estimated_action_cost ASC,
            x.scenario_id
    ) AS comparison_rank

FROM v_latest_recovery_scenario_runs x

WHERE x.scenario_status IN (
    'SIMULATED',
    'PUBLISHED'
);
