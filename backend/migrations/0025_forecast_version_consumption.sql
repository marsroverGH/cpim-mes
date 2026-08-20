-- 0025: Versioned forecasting + forecast consumption
-- Forecasts are no longer persisted as unversioned demand_forecasts(source='FORECAST').
-- Each saved run is immutable after activation/archive. Customer orders consume the
-- active forecast bucket-by-bucket; explicit planner action can publish consumed demand to MPS.

CREATE TABLE IF NOT EXISTS forecast_runs (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id               uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  version               integer NOT NULL CHECK (version > 0),
  scenario              text NOT NULL DEFAULT 'BASE',
  method                text NOT NULL,
  bucket_days           integer NOT NULL CHECK (bucket_days > 0),
  horizon_periods       integer NOT NULL CHECK (horizon_periods > 0),
  as_of_date            date NOT NULL DEFAULT current_date,
  parameters            jsonb NOT NULL DEFAULT '{}'::jsonb,
  mae                   numeric NOT NULL DEFAULT 0,
  mape                  numeric NOT NULL DEFAULT 0,
  status                text NOT NULL DEFAULT 'DRAFT'
                        CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
  generated_at          timestamptz NOT NULL DEFAULT now(),
  generated_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
  generated_by          text NOT NULL DEFAULT '',
  activated_at          timestamptz,
  activated_by_user_id  uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by          text,
  UNIQUE (item_id, scenario, version),
  CONSTRAINT forecast_scenario_normalized_chk CHECK (scenario <> '' AND scenario = upper(btrim(scenario))),
  CONSTRAINT forecast_generation_actor_chk CHECK (method='LEGACY' OR (generated_by_user_id IS NOT NULL AND generated_by <> ''))
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_forecast_runs_one_active
  ON forecast_runs(item_id, scenario)
  WHERE status='ACTIVE';
CREATE INDEX IF NOT EXISTS forecast_runs_item_idx
  ON forecast_runs(item_id, scenario, version DESC);

CREATE TABLE IF NOT EXISTS forecast_values (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  forecast_run_id uuid NOT NULL REFERENCES forecast_runs(id) ON DELETE CASCADE,
  period          date NOT NULL,
  quantity        numeric NOT NULL CHECK (quantity >= 0),
  UNIQUE (forecast_run_id, period)
);
CREATE INDEX IF NOT EXISTS forecast_values_run_period_idx
  ON forecast_values(forecast_run_id, period);

ALTER TABLE mps_entries
  ADD COLUMN IF NOT EXISTS source_forecast_run_id uuid REFERENCES forecast_runs(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS demand_basis text NOT NULL DEFAULT 'MANUAL';

DO $$ BEGIN
  ALTER TABLE mps_entries ADD CONSTRAINT mps_demand_basis_chk
    CHECK (demand_basis IN ('MANUAL','FORECAST_CONSUMPTION'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
  ALTER TABLE mps_entries ADD CONSTRAINT mps_forecast_provenance_chk
    CHECK ((demand_basis='MANUAL' AND source_forecast_run_id IS NULL) OR
           (demand_basis='FORECAST_CONSUMPTION' AND source_forecast_run_id IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE OR REPLACE FUNCTION guard_mps_forecast_provenance() RETURNS trigger AS $$
DECLARE v_status text; v_item uuid;
BEGIN
  IF NEW.demand_basis='FORECAST_CONSUMPTION' THEN
    SELECT status,item_id INTO v_status,v_item FROM forecast_runs WHERE id=NEW.source_forecast_run_id;
    IF NOT FOUND OR v_status <> 'ACTIVE' OR v_item IS DISTINCT FROM NEW.item_id THEN
      RAISE EXCEPTION 'MPS forecast provenance must reference ACTIVE forecast run for the same item' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS mps_forecast_provenance_guard_trg ON mps_entries;
CREATE TRIGGER mps_forecast_provenance_guard_trg
  BEFORE INSERT OR UPDATE OF item_id,planned,source_forecast_run_id,demand_basis ON mps_entries
  FOR EACH ROW EXECUTE FUNCTION guard_mps_forecast_provenance();

-- New forecasts must be versioned. Historical pre-0025 FORECAST rows remain for audit,
-- but new unversioned FORECAST demand rows are blocked even for direct SQL callers.
CREATE OR REPLACE FUNCTION guard_unversioned_forecast_demand() RETURNS trigger AS $$
BEGIN
  IF TG_OP='INSERT' AND NEW.source <> 'ORDER' THEN
    RAISE EXCEPTION 'new demand_forecasts rows must be ORDER; use forecast_runs for forecasts' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND NEW.source='FORECAST' THEN
    RAISE EXCEPTION 'legacy unversioned FORECAST rows are immutable; use forecast_runs' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS unversioned_forecast_demand_guard_trg ON demand_forecasts;
CREATE TRIGGER unversioned_forecast_demand_guard_trg
  BEFORE INSERT OR UPDATE ON demand_forecasts
  FOR EACH ROW EXECUTE FUNCTION guard_unversioned_forecast_demand();

-- Immutable run values once the run leaves DRAFT.
CREATE OR REPLACE FUNCTION guard_forecast_values_mutation() RETURNS trigger AS $$
DECLARE v_status text; v_run_id uuid; v_as_of date;
BEGIN
  IF TG_OP='DELETE' THEN
    v_run_id := OLD.forecast_run_id;
  ELSE
    v_run_id := NEW.forecast_run_id;
  END IF;
  SELECT status, as_of_date INTO v_status, v_as_of FROM forecast_runs WHERE id=v_run_id;
  IF v_status IS DISTINCT FROM 'DRAFT' THEN
    RAISE EXCEPTION 'forecast values are immutable when run status is %', v_status USING ERRCODE='23514';
  END IF;
  IF TG_OP <> 'DELETE' AND NEW.period < v_as_of THEN
    RAISE EXCEPTION 'forecast value period % is before run as_of_date %', NEW.period, v_as_of USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS forecast_values_mutation_guard_trg ON forecast_values;
CREATE TRIGGER forecast_values_mutation_guard_trg
  BEFORE INSERT OR UPDATE OR DELETE ON forecast_values
  FOR EACH ROW EXECUTE FUNCTION guard_forecast_values_mutation();

-- Status transitions and activation actor are DB-enforced as well as Backend-enforced.
CREATE OR REPLACE FUNCTION guard_forecast_run_transition() RETURNS trigger AS $$
DECLARE v_username text; v_role text; v_active boolean; v_activation_needed boolean := false;
BEGIN
  IF TG_OP='INSERT' AND NEW.generated_by_user_id IS NOT NULL THEN
    SELECT username, role, is_active INTO v_username, v_role, v_active
      FROM users WHERE id=NEW.generated_by_user_id;
    IF NOT FOUND OR NOT v_active OR v_username IS DISTINCT FROM NEW.generated_by OR v_role NOT IN ('planner','admin') THEN
      RAISE EXCEPTION 'invalid forecast generation actor' USING ERRCODE='23514';
    END IF;
  END IF;
  IF TG_OP='UPDATE' THEN
    IF NEW.generated_at IS DISTINCT FROM OLD.generated_at OR
       NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR
       NEW.generated_by IS DISTINCT FROM OLD.generated_by THEN
      RAISE EXCEPTION 'forecast generation audit fields are immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status <> 'DRAFT' AND (
      NEW.item_id IS DISTINCT FROM OLD.item_id OR NEW.version IS DISTINCT FROM OLD.version OR
      NEW.scenario IS DISTINCT FROM OLD.scenario OR NEW.method IS DISTINCT FROM OLD.method OR
      NEW.bucket_days IS DISTINCT FROM OLD.bucket_days OR NEW.horizon_periods IS DISTINCT FROM OLD.horizon_periods OR
      NEW.as_of_date IS DISTINCT FROM OLD.as_of_date OR NEW.parameters IS DISTINCT FROM OLD.parameters OR
      NEW.mae IS DISTINCT FROM OLD.mae OR NEW.mape IS DISTINCT FROM OLD.mape OR
      NEW.generated_at IS DISTINCT FROM OLD.generated_at OR NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR
      NEW.generated_by IS DISTINCT FROM OLD.generated_by OR NEW.activated_at IS DISTINCT FROM OLD.activated_at OR
      NEW.activated_by_user_id IS DISTINCT FROM OLD.activated_by_user_id OR NEW.activated_by IS DISTINCT FROM OLD.activated_by
    ) THEN
      RAISE EXCEPTION 'activated/archived forecast run metadata is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status='ARCHIVED' AND NEW.status IS DISTINCT FROM OLD.status THEN
      RAISE EXCEPTION 'ARCHIVED forecast run is terminal' USING ERRCODE='23514';
    END IF;
    IF OLD.status='ACTIVE' AND NEW.status NOT IN ('ACTIVE','ARCHIVED') THEN
      RAISE EXCEPTION 'ACTIVE forecast run may only remain ACTIVE or become ARCHIVED' USING ERRCODE='23514';
    END IF;
    IF OLD.status='DRAFT' AND NEW.status NOT IN ('DRAFT','ACTIVE','ARCHIVED') THEN
      RAISE EXCEPTION 'invalid DRAFT forecast transition to %', NEW.status USING ERRCODE='23514';
    END IF;
  END IF;

  IF NEW.status='ACTIVE' THEN
    IF TG_OP='INSERT' THEN
      v_activation_needed := true;
    ELSIF OLD.status IS DISTINCT FROM 'ACTIVE' THEN
      v_activation_needed := true;
    END IF;
  END IF;
  IF v_activation_needed THEN
    IF NEW.activated_by_user_id IS NULL OR COALESCE(NEW.activated_by,'')='' OR NEW.activated_at IS NULL THEN
      RAISE EXCEPTION 'forecast activation actor/timestamp required' USING ERRCODE='23514';
    END IF;
    SELECT username, role, is_active INTO v_username, v_role, v_active
      FROM users WHERE id=NEW.activated_by_user_id;
    IF NOT FOUND OR NOT v_active OR v_username IS DISTINCT FROM NEW.activated_by OR v_role NOT IN ('planner','admin') THEN
      RAISE EXCEPTION 'invalid forecast activation actor' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS forecast_run_transition_guard_trg ON forecast_runs;
CREATE TRIGGER forecast_run_transition_guard_trg
  BEFORE INSERT OR UPDATE ON forecast_runs
  FOR EACH ROW EXECUTE FUNCTION guard_forecast_run_transition();

CREATE OR REPLACE FUNCTION guard_forecast_run_delete() RETURNS trigger AS $$
BEGIN
  IF OLD.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'ACTIVE/ARCHIVED forecast versions cannot be deleted' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS forecast_run_delete_guard_trg ON forecast_runs;
CREATE TRIGGER forecast_run_delete_guard_trg
  BEFORE DELETE ON forecast_runs
  FOR EACH ROW EXECUTE FUNCTION guard_forecast_run_delete();

-- Preserve pre-0025 unversioned forecasts as a LEGACY archived run per item.
DO $$
DECLARE r record; run_id uuid;
BEGIN
  FOR r IN
    SELECT item_id FROM demand_forecasts WHERE source='FORECAST' GROUP BY item_id
  LOOP
    IF NOT EXISTS (SELECT 1 FROM forecast_runs WHERE item_id=r.item_id AND scenario='LEGACY') THEN
      run_id := gen_random_uuid();
      INSERT INTO forecast_runs(
        id,item_id,version,scenario,method,bucket_days,horizon_periods,as_of_date,parameters,
        mae,mape,status,generated_at,generated_by
      )
      SELECT run_id,r.item_id,1,'LEGACY','LEGACY',1,
             GREATEST(count(DISTINCT due_date),1),MIN(due_date),'{}'::jsonb,0,0,'DRAFT',
             COALESCE(min(created_at),now()),'legacy-migration'
        FROM demand_forecasts WHERE item_id=r.item_id AND source='FORECAST';

      INSERT INTO forecast_values(forecast_run_id,period,quantity)
      SELECT run_id,due_date,SUM(quantity)
        FROM demand_forecasts
       WHERE item_id=r.item_id AND source='FORECAST'
       GROUP BY due_date;

      UPDATE forecast_runs SET status='ARCHIVED' WHERE id=run_id;
    END IF;
  END LOOP;
END $$;

COMMENT ON TABLE forecast_runs IS 'Immutable version metadata for statistical forecast runs';
COMMENT ON TABLE forecast_values IS 'Future forecast quantities for one version; consumed dynamically against customer orders';
COMMENT ON COLUMN mps_entries.source_forecast_run_id IS 'Forecast version used when MPS planned demand was published from consumption';
