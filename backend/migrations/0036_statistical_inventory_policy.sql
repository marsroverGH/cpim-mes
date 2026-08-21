-- 0036: Statistical Safety Stock / Reorder Point / Min-Max Inventory Policy
--
-- Configuration is versioned and immutable after activation. Calculated values are
-- append-only snapshots so planners can distinguish a policy change from changing
-- demand / supplier lead-time evidence.

CREATE TABLE inventory_policy_versions (
  id                     uuid PRIMARY KEY,
  item_id                uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  version_no             integer NOT NULL CHECK (version_no > 0),
  status                 text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','ACTIVE','ARCHIVED')),
  policy_method          text NOT NULL DEFAULT 'STATISTICAL' CHECK (policy_method IN ('STATISTICAL','FIXED')),
  replenishment_method   text NOT NULL DEFAULT 'MIN_MAX' CHECK (replenishment_method IN ('SAFETY_STOCK','MIN_MAX')),
  service_level          numeric(7,6) NOT NULL DEFAULT 0.950000 CHECK (service_level BETWEEN 0.500000 AND 0.999900),
  demand_window_days     integer NOT NULL DEFAULT 90 CHECK (demand_window_days BETWEEN 7 AND 730),
  min_history_days       integer NOT NULL DEFAULT 30 CHECK (min_history_days BETWEEN 1 AND 730),
  order_cycle_days       integer NOT NULL DEFAULT 14 CHECK (order_cycle_days BETWEEN 0 AND 365),
  fixed_safety_stock     numeric CHECK (fixed_safety_stock IS NULL OR fixed_safety_stock >= 0),
  effective_from         date NOT NULL DEFAULT eco_business_date(now()),
  notes                  text NOT NULL DEFAULT '',
  created_by_user_id     uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_by             text NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  activated_by_user_id   uuid REFERENCES users(id) ON DELETE RESTRICT,
  activated_by           text,
  activated_at           timestamptz,
  archived_by_user_id    uuid REFERENCES users(id) ON DELETE RESTRICT,
  archived_by            text,
  archived_at            timestamptz,
  UNIQUE(item_id,version_no),
  CHECK (min_history_days <= demand_window_days),
  CHECK (policy_method <> 'FIXED' OR fixed_safety_stock IS NOT NULL)
);
CREATE UNIQUE INDEX ux_inventory_policy_one_active
  ON inventory_policy_versions(item_id) WHERE status='ACTIVE';
CREATE INDEX inventory_policy_item_versions_idx
  ON inventory_policy_versions(item_id,version_no DESC);

CREATE OR REPLACE FUNCTION guard_inventory_policy_actor(p_user uuid,p_name text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE urole text; uname text; active boolean;
BEGIN
  SELECT role,username,is_active INTO urole,uname,active FROM users WHERE id=p_user;
  IF urole IS NULL OR NOT active OR urole NOT IN ('planner','admin') OR uname IS DISTINCT FROM p_name THEN
    RAISE EXCEPTION 'inventory policy mutation requires matching active planner/admin actor' USING ERRCODE='42501';
  END IF;
END$$;

CREATE OR REPLACE FUNCTION guard_inventory_policy_version()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    PERFORM guard_inventory_policy_actor(NEW.created_by_user_id,NEW.created_by);
    IF NEW.status<>'DRAFT' OR NEW.activated_at IS NOT NULL OR NEW.archived_at IS NOT NULL THEN
      RAISE EXCEPTION 'inventory policy version must be created as DRAFT' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
  END IF;
  IF TG_OP='DELETE' THEN
    IF OLD.status<>'DRAFT' THEN
      RAISE EXCEPTION 'active/archived inventory policy versions cannot be deleted' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.item_id IS DISTINCT FROM OLD.item_id OR
     NEW.version_no IS DISTINCT FROM OLD.version_no OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR
     NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'inventory policy identity/audit fields are immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status<>'DRAFT' AND (
     NEW.policy_method IS DISTINCT FROM OLD.policy_method OR NEW.replenishment_method IS DISTINCT FROM OLD.replenishment_method OR
     NEW.service_level IS DISTINCT FROM OLD.service_level OR NEW.demand_window_days IS DISTINCT FROM OLD.demand_window_days OR
     NEW.min_history_days IS DISTINCT FROM OLD.min_history_days OR NEW.order_cycle_days IS DISTINCT FROM OLD.order_cycle_days OR
     NEW.fixed_safety_stock IS DISTINCT FROM OLD.fixed_safety_stock OR NEW.effective_from IS DISTINCT FROM OLD.effective_from OR
     NEW.notes IS DISTINCT FROM OLD.notes) THEN
    RAISE EXCEPTION 'active/archived inventory policy configuration is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status='ARCHIVED' THEN
    RAISE EXCEPTION 'ARCHIVED inventory policy is terminal' USING ERRCODE='23514';
  END IF;
  IF OLD.status='DRAFT' AND NEW.status='ACTIVE' THEN
    IF NEW.effective_from > eco_business_date(now()) THEN
      RAISE EXCEPTION 'future-effective inventory policy must remain DRAFT until effective date' USING ERRCODE='23514';
    END IF;
    IF NEW.activated_by_user_id IS NULL OR COALESCE(NEW.activated_by,'')='' OR NEW.activated_at IS NULL THEN
      RAISE EXCEPTION 'activation actor/timestamp required' USING ERRCODE='23514';
    END IF;
    PERFORM guard_inventory_policy_actor(NEW.activated_by_user_id,NEW.activated_by);
  ELSIF OLD.status='ACTIVE' AND NEW.status='ARCHIVED' THEN
    IF NEW.archived_by_user_id IS NULL OR COALESCE(NEW.archived_by,'')='' OR NEW.archived_at IS NULL THEN
      RAISE EXCEPTION 'archive actor/timestamp required' USING ERRCODE='23514';
    END IF;
    PERFORM guard_inventory_policy_actor(NEW.archived_by_user_id,NEW.archived_by);
  ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
    RAISE EXCEPTION 'invalid inventory policy transition % -> %',OLD.status,NEW.status USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;

CREATE TRIGGER inventory_policy_version_guard_trg
BEFORE INSERT OR UPDATE OR DELETE ON inventory_policy_versions
FOR EACH ROW EXECUTE FUNCTION guard_inventory_policy_version();

CREATE TABLE inventory_policy_runs (
  id                     uuid PRIMARY KEY,
  as_of_date             date NOT NULL,
  status                 text NOT NULL CHECK (status IN ('RUNNING','COMPLETE','FAILED')),
  result_hash            text CHECK (result_hash IS NULL OR length(result_hash)=64),
  generated_by_user_id   uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  generated_by           text NOT NULL,
  completed_at           timestamptz,
  error_text             text NOT NULL DEFAULT '',
  created_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inventory_policy_runs_created_idx ON inventory_policy_runs(created_at DESC,id DESC);

CREATE TABLE inventory_policy_results (
  id                       uuid PRIMARY KEY,
  run_id                   uuid NOT NULL REFERENCES inventory_policy_runs(id) ON DELETE RESTRICT,
  policy_version_id        uuid NOT NULL REFERENCES inventory_policy_versions(id) ON DELETE RESTRICT,
  item_id                  uuid NOT NULL REFERENCES items(id) ON DELETE RESTRICT,
  demand_observation_days  integer NOT NULL CHECK (demand_observation_days >= 0),
  nonzero_demand_days      integer NOT NULL CHECK (nonzero_demand_days >= 0),
  average_daily_demand     numeric(18,6) NOT NULL CHECK (average_daily_demand >= 0),
  stddev_daily_demand      numeric(18,6) NOT NULL CHECK (stddev_daily_demand >= 0),
  lead_time_mean_days      numeric(12,4) NOT NULL CHECK (lead_time_mean_days >= 0),
  lead_time_stddev_days    numeric(12,4) NOT NULL CHECK (lead_time_stddev_days >= 0),
  service_level            numeric(7,6) NOT NULL CHECK (service_level BETWEEN 0.500000 AND 0.999900),
  z_value                  numeric(12,6) NOT NULL CHECK (z_value >= 0),
  safety_stock             numeric(18,6) NOT NULL CHECK (safety_stock >= 0),
  reorder_point            numeric(18,6) NOT NULL CHECK (reorder_point >= 0),
  min_qty                  numeric(18,6) NOT NULL CHECK (min_qty >= 0),
  max_qty                  numeric(18,6) NOT NULL CHECK (max_qty >= min_qty),
  demand_source            text NOT NULL,
  lead_time_source         text NOT NULL,
  confidence               text NOT NULL CHECK (confidence IN ('LOW','MEDIUM','HIGH')),
  created_at               timestamptz NOT NULL DEFAULT now(),
  UNIQUE(run_id,policy_version_id),
  UNIQUE(run_id,item_id)
);
CREATE INDEX inventory_policy_results_item_idx ON inventory_policy_results(item_id,run_id);

CREATE OR REPLACE FUNCTION guard_inventory_policy_run_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM guard_inventory_policy_actor(NEW.generated_by_user_id,NEW.generated_by);
  IF NEW.status<>'RUNNING' OR NEW.result_hash IS NOT NULL OR NEW.completed_at IS NOT NULL THEN
    RAISE EXCEPTION 'inventory policy run must start RUNNING' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER inventory_policy_runs_insert_guard_trg
BEFORE INSERT ON inventory_policy_runs
FOR EACH ROW EXECUTE FUNCTION guard_inventory_policy_run_insert();

CREATE OR REPLACE FUNCTION guard_inventory_policy_run_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'inventory policy runs are append-only' USING ERRCODE='23514';
  END IF;
  IF OLD.status<>'RUNNING' THEN
    RAISE EXCEPTION 'completed inventory policy run is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.as_of_date IS DISTINCT FROM OLD.as_of_date OR
     NEW.generated_by_user_id IS DISTINCT FROM OLD.generated_by_user_id OR NEW.generated_by IS DISTINCT FROM OLD.generated_by OR
     NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'inventory policy run request/audit fields are immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status NOT IN ('COMPLETE','FAILED') THEN
    RAISE EXCEPTION 'inventory policy run may only transition RUNNING -> COMPLETE/FAILED' USING ERRCODE='23514';
  END IF;
  IF NEW.status='COMPLETE' AND (NEW.result_hash IS NULL OR length(NEW.result_hash)<>64 OR NEW.completed_at IS NULL) THEN
    RAISE EXCEPTION 'completed inventory policy run requires result_hash and completed_at' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER inventory_policy_runs_guard_trg
BEFORE UPDATE OR DELETE ON inventory_policy_runs
FOR EACH ROW EXECUTE FUNCTION guard_inventory_policy_run_mutation();

CREATE OR REPLACE FUNCTION guard_inventory_policy_result_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rs text; ps text; pi uuid;
BEGIN
  SELECT status INTO rs FROM inventory_policy_runs WHERE id=NEW.run_id FOR SHARE;
  SELECT status,item_id INTO ps,pi FROM inventory_policy_versions WHERE id=NEW.policy_version_id FOR SHARE;
  IF rs IS DISTINCT FROM 'RUNNING' THEN
    RAISE EXCEPTION 'inventory policy results may only be inserted while run is RUNNING' USING ERRCODE='23514';
  END IF;
  IF ps IS DISTINCT FROM 'ACTIVE' OR pi IS DISTINCT FROM NEW.item_id THEN
    RAISE EXCEPTION 'inventory policy result must reference ACTIVE policy for same item' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END$$;
CREATE TRIGGER inventory_policy_results_insert_guard_trg
BEFORE INSERT ON inventory_policy_results
FOR EACH ROW EXECUTE FUNCTION guard_inventory_policy_result_insert();

CREATE OR REPLACE FUNCTION reject_inventory_policy_result_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'inventory policy results are append-only evidence' USING ERRCODE='23514';
END$$;
CREATE TRIGGER inventory_policy_results_append_only_trg
BEFORE UPDATE OR DELETE ON inventory_policy_results
FOR EACH ROW EXECUTE FUNCTION reject_inventory_policy_result_mutation();

-- Latest completed calculation for each currently ACTIVE policy version. Until a
-- statistical policy has its first completed calculation, the legacy item-master
-- safety_stock remains the conservative compatibility fallback.
CREATE OR REPLACE VIEW v_current_inventory_policy AS
SELECT v.id AS policy_version_id,v.item_id,v.version_no,v.status,v.policy_method,v.replenishment_method,
       v.service_level::double precision AS service_level,v.demand_window_days,v.min_history_days,v.order_cycle_days,
       v.fixed_safety_stock::double precision AS fixed_safety_stock,v.effective_from,v.notes,v.activated_at,
       r.id AS result_id,r.run_id,r.demand_observation_days,r.nonzero_demand_days,
       r.average_daily_demand::double precision AS average_daily_demand,
       r.stddev_daily_demand::double precision AS stddev_daily_demand,
       r.lead_time_mean_days::double precision AS lead_time_mean_days,
       r.lead_time_stddev_days::double precision AS lead_time_stddev_days,
       r.z_value::double precision AS z_value,
       COALESCE(r.safety_stock::double precision,v.fixed_safety_stock::double precision,i.safety_stock::double precision,0) AS safety_stock,
       COALESCE(r.reorder_point::double precision,v.fixed_safety_stock::double precision,i.safety_stock::double precision,0) AS reorder_point,
       COALESCE(r.min_qty::double precision,v.fixed_safety_stock::double precision,i.safety_stock::double precision,0) AS min_qty,
       COALESCE(r.max_qty::double precision,v.fixed_safety_stock::double precision,i.safety_stock::double precision,0) AS max_qty,
       COALESCE(r.demand_source,'ITEM_MASTER_FALLBACK') AS demand_source,
       COALESCE(r.lead_time_source,'ITEM_MASTER') AS lead_time_source,
       COALESCE(r.confidence,'LOW') AS confidence,
       CASE WHEN r.id IS NULL THEN 'FALLBACK' ELSE 'CALCULATED' END AS calculation_status,
       rr.as_of_date AS calculated_as_of,rr.created_at AS calculated_at
  FROM inventory_policy_versions v
  JOIN items i ON i.id=v.item_id
  LEFT JOIN LATERAL (
    SELECT x.* FROM inventory_policy_results x
    JOIN inventory_policy_runs rr0 ON rr0.id=x.run_id AND rr0.status='COMPLETE'
    WHERE x.policy_version_id=v.id
    ORDER BY rr0.created_at DESC,rr0.id DESC LIMIT 1
  ) r ON true
  LEFT JOIN inventory_policy_runs rr ON rr.id=r.run_id
 WHERE v.status='ACTIVE' AND v.effective_from<=eco_business_date(now());

-- 0034 graph vocabulary / exception vocabulary extensions.
ALTER TABLE pegging_nodes DROP CONSTRAINT IF EXISTS pegging_nodes_node_type_check;
ALTER TABLE pegging_nodes ADD CONSTRAINT pegging_nodes_node_type_check CHECK (node_type IN (
  'SALES_ORDER','SALES_ORDER_LINE','PROMISE','BACKORDER','INVENTORY',
  'ITEM','WORK_ORDER','PLANNED_ORDER','PURCHASE_ORDER','SUPPLIER',
  'QUALITY_HOLD','DETAILED_SCHEDULE','WORK_CENTER','SHORTAGE',
  'SUPPLIER_CONFIRMATION','SUPPLIER_ASN','LEAD_TIME_PROFILE','INVENTORY_POLICY'
));

ALTER TABLE pegging_edges DROP CONSTRAINT IF EXISTS pegging_edges_edge_type_check;
ALTER TABLE pegging_edges ADD CONSTRAINT pegging_edges_edge_type_check CHECK (edge_type IN (
  'HAS_LINE','PROMISED_BY','REPRIORITIZED_BY','ALLOCATED_FROM','SUPPLIED_BY',
  'REQUIRES_COMPONENT','PRODUCED_BY','PURCHASED_BY','PLANNED_SUPPLY',
  'SCHEDULED_BY','USES_WORK_CENTER','BLOCKED_BY','SHORT_BY',
  'CONFIRMED_BY','SHIPPED_BY','PLANNED_USING','PROTECTED_BY'
));

ALTER TABLE planning_exceptions DROP CONSTRAINT IF EXISTS planning_exceptions_exception_type_check;
ALTER TABLE planning_exceptions ADD CONSTRAINT planning_exceptions_exception_type_check CHECK (exception_type IN (
  'LATE_PROMISE','BACKORDER','UNCONVERTED_CTP','MATERIAL_SHORTAGE',
  'LATE_PURCHASE_ORDER','SUPPLIER_BLOCKED','QUALITY_HOLD',
  'LATE_WORK_ORDER','CAPACITY_LATE','CAPACITY_UNSCHEDULED',
  'SUPPLIER_CONFIRMATION_LATE','SUPPLIER_RELIABILITY_RISK',
  'SAFETY_STOCK_BREACH','REORDER_POINT_BREACH'
));
