-- 0026: S&OP -> MPS product-mix disaggregation
-- Family-level monthly supply plans are converted into item-level time-phased MPS
-- through an explicitly versioned, auditable product mix.

CREATE TABLE IF NOT EXISTS sop_product_mix_versions (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id             uuid NOT NULL REFERENCES item_groups(id) ON DELETE CASCADE,
  version              integer NOT NULL CHECK (version > 0),
  name                 text NOT NULL DEFAULT '',
  status               text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
  created_at           timestamptz NOT NULL DEFAULT now(),
  created_by_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  created_by           text NOT NULL DEFAULT '',
  activated_at         timestamptz,
  activated_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by         text,
  UNIQUE(group_id, version)
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_sop_product_mix_one_active
  ON sop_product_mix_versions(group_id) WHERE status='ACTIVE';

CREATE TABLE IF NOT EXISTS sop_product_mix_lines (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  mix_version_id uuid NOT NULL REFERENCES sop_product_mix_versions(id) ON DELETE CASCADE,
  item_id        uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  mix_pct        numeric NOT NULL CHECK (mix_pct > 0 AND mix_pct <= 100),
  UNIQUE(mix_version_id, item_id)
);

CREATE TABLE IF NOT EXISTS sop_disaggregation_runs (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sop_plan_id             uuid NOT NULL REFERENCES sop_plans(id) ON DELETE RESTRICT,
  mix_version_id          uuid NOT NULL REFERENCES sop_product_mix_versions(id) ON DELETE RESTRICT,
  group_id                uuid NOT NULL REFERENCES item_groups(id) ON DELETE RESTRICT,
  plan_month              date NOT NULL,
  supply_qty_snapshot     numeric NOT NULL CHECK (supply_qty_snapshot >= 0),
  time_phasing            text NOT NULL DEFAULT 'CALENDAR_DAYS_7D' CHECK (time_phasing='CALENDAR_DAYS_7D'),
  status                  text NOT NULL DEFAULT 'APPLIED' CHECK (status='APPLIED'),
  applied_at              timestamptz NOT NULL DEFAULT now(),
  applied_by_user_id      uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  applied_by              text NOT NULL,
  created_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sop_disaggregation_runs_plan_idx
  ON sop_disaggregation_runs(sop_plan_id, applied_at DESC);

CREATE TABLE IF NOT EXISTS sop_disaggregation_lines (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id         uuid NOT NULL REFERENCES sop_disaggregation_runs(id) ON DELETE CASCADE,
  item_id        uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  period         date NOT NULL,
  mix_pct        numeric NOT NULL CHECK (mix_pct > 0 AND mix_pct <= 100),
  time_weight    numeric NOT NULL CHECK (time_weight > 0 AND time_weight <= 1),
  planned_qty    numeric NOT NULL CHECK (planned_qty >= 0),
  UNIQUE(run_id, item_id, period)
);

ALTER TABLE mps_entries
  ADD COLUMN IF NOT EXISTS source_sop_plan_id uuid REFERENCES sop_plans(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS source_sop_disaggregation_run_id uuid REFERENCES sop_disaggregation_runs(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS source_product_mix_version_id uuid REFERENCES sop_product_mix_versions(id) ON DELETE SET NULL;

ALTER TABLE mps_entries DROP CONSTRAINT IF EXISTS mps_demand_basis_chk;
ALTER TABLE mps_entries ADD CONSTRAINT mps_demand_basis_chk
  CHECK (demand_basis IN ('MANUAL','FORECAST_CONSUMPTION','SOP_DISAGGREGATION'));

ALTER TABLE mps_entries DROP CONSTRAINT IF EXISTS mps_forecast_provenance_chk;
ALTER TABLE mps_entries ADD CONSTRAINT mps_planning_provenance_chk CHECK (
  (demand_basis='MANUAL' AND source_forecast_run_id IS NULL AND source_sop_plan_id IS NULL AND source_sop_disaggregation_run_id IS NULL AND source_product_mix_version_id IS NULL)
  OR
  (demand_basis='FORECAST_CONSUMPTION' AND source_forecast_run_id IS NOT NULL AND source_sop_plan_id IS NULL AND source_sop_disaggregation_run_id IS NULL AND source_product_mix_version_id IS NULL)
  OR
  (demand_basis='SOP_DISAGGREGATION' AND source_forecast_run_id IS NULL AND source_sop_plan_id IS NOT NULL AND source_sop_disaggregation_run_id IS NOT NULL AND source_product_mix_version_id IS NOT NULL)
);

CREATE OR REPLACE FUNCTION guard_sop_mix_line() RETURNS trigger AS $$
DECLARE v_group uuid; v_status text; v_item_group uuid; v_type text;
BEGIN
  SELECT group_id,status INTO v_group,v_status FROM sop_product_mix_versions WHERE id=NEW.mix_version_id;
  IF NOT FOUND OR v_status <> 'DRAFT' THEN
    RAISE EXCEPTION 'product mix lines may only be changed while version is DRAFT' USING ERRCODE='23514';
  END IF;
  SELECT group_id,type INTO v_item_group,v_type FROM items WHERE id=NEW.item_id;
  IF NOT FOUND OR v_item_group IS DISTINCT FROM v_group OR v_type NOT IN ('FG','SA') THEN
    RAISE EXCEPTION 'product mix item must be an FG/SA member of the mix family' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS sop_mix_line_guard_trg ON sop_product_mix_lines;
CREATE TRIGGER sop_mix_line_guard_trg
  BEFORE INSERT OR UPDATE ON sop_product_mix_lines
  FOR EACH ROW EXECUTE FUNCTION guard_sop_mix_line();

CREATE OR REPLACE FUNCTION guard_sop_mix_line_delete() RETURNS trigger AS $$
DECLARE v_status text;
BEGIN
  SELECT status INTO v_status FROM sop_product_mix_versions WHERE id=OLD.mix_version_id;
  IF v_status IS DISTINCT FROM 'DRAFT' THEN
    RAISE EXCEPTION 'product mix lines are immutable after activation/archive' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS sop_mix_line_delete_guard_trg ON sop_product_mix_lines;
CREATE TRIGGER sop_mix_line_delete_guard_trg
  BEFORE DELETE ON sop_product_mix_lines
  FOR EACH ROW EXECUTE FUNCTION guard_sop_mix_line_delete();

CREATE OR REPLACE FUNCTION guard_sop_mix_version() RETURNS trigger AS $$
DECLARE v_sum numeric; v_username text; v_role text; v_active boolean;
BEGIN
  IF TG_OP='INSERT' AND NEW.created_by_user_id IS NOT NULL THEN
    SELECT username,role,is_active INTO v_username,v_role,v_active FROM users WHERE id=NEW.created_by_user_id;
    IF NOT FOUND OR NOT v_active OR v_username IS DISTINCT FROM NEW.created_by OR v_role NOT IN ('planner','admin') THEN
      RAISE EXCEPTION 'invalid product mix creation actor' USING ERRCODE='23514';
    END IF;
  END IF;
  IF TG_OP='UPDATE' THEN
    IF NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
      RAISE EXCEPTION 'product mix creation audit fields are immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status <> 'DRAFT' AND (NEW.group_id IS DISTINCT FROM OLD.group_id OR NEW.version IS DISTINCT FROM OLD.version OR NEW.name IS DISTINCT FROM OLD.name) THEN
      RAISE EXCEPTION 'active/archived product mix metadata is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status='ARCHIVED' AND NEW.status IS DISTINCT FROM OLD.status THEN
      RAISE EXCEPTION 'ARCHIVED product mix is terminal' USING ERRCODE='23514';
    END IF;
    IF OLD.status='ACTIVE' AND NEW.status NOT IN ('ACTIVE','ARCHIVED') THEN
      RAISE EXCEPTION 'ACTIVE product mix may only remain ACTIVE or become ARCHIVED' USING ERRCODE='23514';
    END IF;
    IF OLD.status='DRAFT' AND NEW.status='ACTIVE' THEN
      SELECT COALESCE(SUM(mix_pct),0) INTO v_sum FROM sop_product_mix_lines WHERE mix_version_id=NEW.id;
      IF abs(v_sum - 100) > 0.000001 THEN
        RAISE EXCEPTION 'product mix total must equal 100%%; actual %', v_sum USING ERRCODE='23514';
      END IF;
      IF NEW.activated_by_user_id IS NULL OR COALESCE(NEW.activated_by,'')='' OR NEW.activated_at IS NULL THEN
        RAISE EXCEPTION 'product mix activation actor/timestamp required' USING ERRCODE='23514';
      END IF;
      SELECT username,role,is_active INTO v_username,v_role,v_active FROM users WHERE id=NEW.activated_by_user_id;
      IF NOT FOUND OR NOT v_active OR v_username IS DISTINCT FROM NEW.activated_by OR v_role NOT IN ('planner','admin') THEN
        RAISE EXCEPTION 'invalid product mix activation actor' USING ERRCODE='23514';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS sop_mix_version_guard_trg ON sop_product_mix_versions;
CREATE TRIGGER sop_mix_version_guard_trg
  BEFORE INSERT OR UPDATE ON sop_product_mix_versions
  FOR EACH ROW EXECUTE FUNCTION guard_sop_mix_version();

CREATE OR REPLACE FUNCTION guard_sop_disaggregation_insert() RETURNS trigger AS $$
DECLARE v_username text; v_role text; v_active boolean; v_plan_group uuid; v_plan_month date; v_supply numeric; v_mix_group uuid; v_mix_status text;
BEGIN
  NEW.applied_at := transaction_timestamp();
  NEW.created_at := transaction_timestamp();
  SELECT username,role,is_active INTO v_username,v_role,v_active FROM users WHERE id=NEW.applied_by_user_id;
  IF NOT FOUND OR NOT v_active OR v_username IS DISTINCT FROM NEW.applied_by OR v_role NOT IN ('planner','admin') THEN
    RAISE EXCEPTION 'invalid S&OP disaggregation actor' USING ERRCODE='23514';
  END IF;
  SELECT group_id,plan_month,supply_qty INTO v_plan_group,v_plan_month,v_supply FROM sop_plans WHERE id=NEW.sop_plan_id;
  SELECT group_id,status INTO v_mix_group,v_mix_status FROM sop_product_mix_versions WHERE id=NEW.mix_version_id;
  IF v_plan_group IS DISTINCT FROM NEW.group_id OR v_mix_group IS DISTINCT FROM NEW.group_id OR v_mix_status <> 'ACTIVE' THEN
    RAISE EXCEPTION 'S&OP plan/product mix family or status mismatch' USING ERRCODE='23514';
  END IF;
  IF v_plan_month IS DISTINCT FROM NEW.plan_month OR abs(v_supply-NEW.supply_qty_snapshot) > 0.000001 THEN
    RAISE EXCEPTION 'S&OP disaggregation snapshot does not match source plan' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS sop_disagg_insert_guard_trg ON sop_disaggregation_runs;
CREATE TRIGGER sop_disagg_insert_guard_trg BEFORE INSERT ON sop_disaggregation_runs
  FOR EACH ROW EXECUTE FUNCTION guard_sop_disaggregation_insert();

CREATE OR REPLACE FUNCTION guard_sop_disaggregation_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'S&OP disaggregation audit rows are immutable' USING ERRCODE='23514';
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS sop_disagg_run_immutable_trg ON sop_disaggregation_runs;
CREATE TRIGGER sop_disagg_run_immutable_trg BEFORE UPDATE OR DELETE ON sop_disaggregation_runs
  FOR EACH ROW EXECUTE FUNCTION guard_sop_disaggregation_immutable();
DROP TRIGGER IF EXISTS sop_disagg_line_immutable_trg ON sop_disaggregation_lines;
CREATE TRIGGER sop_disagg_line_immutable_trg BEFORE UPDATE OR DELETE ON sop_disaggregation_lines
  FOR EACH ROW EXECUTE FUNCTION guard_sop_disaggregation_immutable();

-- Extend the existing forecast provenance trigger to validate S&OP provenance too.
CREATE OR REPLACE FUNCTION guard_mps_forecast_provenance() RETURNS trigger AS $$
DECLARE v_status text; v_item uuid; v_plan uuid; v_mix uuid; v_group uuid; v_line_qty numeric;
BEGIN
  IF NEW.demand_basis='FORECAST_CONSUMPTION' THEN
    SELECT status,item_id INTO v_status,v_item FROM forecast_runs WHERE id=NEW.source_forecast_run_id;
    IF NOT FOUND OR v_status <> 'ACTIVE' OR v_item IS DISTINCT FROM NEW.item_id THEN
      RAISE EXCEPTION 'MPS forecast provenance must reference ACTIVE forecast run for the same item' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.demand_basis='SOP_DISAGGREGATION' THEN
    SELECT status,sop_plan_id,mix_version_id,group_id INTO v_status,v_plan,v_mix,v_group
      FROM sop_disaggregation_runs WHERE id=NEW.source_sop_disaggregation_run_id;
    IF NOT FOUND OR v_status <> 'APPLIED' OR v_plan IS DISTINCT FROM NEW.source_sop_plan_id OR v_mix IS DISTINCT FROM NEW.source_product_mix_version_id THEN
      RAISE EXCEPTION 'invalid S&OP disaggregation provenance' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM items WHERE id=NEW.item_id AND group_id=v_group) THEN
      RAISE EXCEPTION 'S&OP MPS item is not a member of the source family' USING ERRCODE='23514';
    END IF;
    SELECT planned_qty INTO v_line_qty FROM sop_disaggregation_lines
      WHERE run_id=NEW.source_sop_disaggregation_run_id AND item_id=NEW.item_id AND period=NEW.period;
    IF NOT FOUND OR abs(v_line_qty - NEW.planned) > 0.000001 THEN
      RAISE EXCEPTION 'MPS quantity does not match S&OP disaggregation snapshot' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS mps_forecast_provenance_guard_trg ON mps_entries;
CREATE TRIGGER mps_forecast_provenance_guard_trg
  BEFORE INSERT OR UPDATE OF item_id,period,planned,source_forecast_run_id,demand_basis,source_sop_plan_id,source_sop_disaggregation_run_id,source_product_mix_version_id ON mps_entries
  FOR EACH ROW EXECUTE FUNCTION guard_mps_forecast_provenance();

COMMENT ON TABLE sop_product_mix_versions IS 'Versioned family-to-item product mix used to disaggregate monthly S&OP supply plans';
COMMENT ON TABLE sop_disaggregation_runs IS 'Immutable audit snapshot of one S&OP monthly plan published to item-level MPS';
