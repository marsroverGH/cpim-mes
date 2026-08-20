-- ============================================================
-- 0019: BOM cycle guard + transactional LLC hardening
-- ============================================================
-- Goals:
--   1. Serialize all BOM topology changes, including direct SQL writers.
--   2. Reject every cyclic final BOM at transaction commit.
--   3. Remove the old fixed 100-iteration LLC limit.  A valid acyclic BOM
--      can legally be deeper than 100 levels; convergence is bounded by the
--      number of items instead.
--
-- Backend services also perform an earlier in-transaction cycle check so API
-- callers receive a clear 409 before COMMIT.  These DB guards are defense in
-- depth and protect maintenance SQL / future code paths as well.

BEGIN;

-- Serialize topology writers with the same advisory lock used by the Go service.
CREATE OR REPLACE FUNCTION lock_bom_topology_change()
RETURNS trigger AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('cpim-mes:bom-topology', 0));
  -- Any new topology statement invalidates a prior in-transaction validation.
  PERFORM set_config('cpim_mes.bom_cycle_checked', '0', true);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS bom_topology_serialize ON bom_components;
CREATE TRIGGER bom_topology_serialize
BEFORE INSERT OR UPDATE OR DELETE ON bom_components
FOR EACH STATEMENT
EXECUTE FUNCTION lock_bom_topology_change();

-- Global cycle assertion.  The recursive walk stops expanding a path as soon as
-- it repeats a node, so an already-corrupt graph cannot recurse forever.
CREATE OR REPLACE FUNCTION assert_bom_acyclic()
RETURNS void AS $$
DECLARE
  cycle_path uuid[];
BEGIN
  WITH RECURSIVE walk(root_id, node_id, path, is_cycle) AS (
    SELECT b.parent_id,
           b.child_id,
           ARRAY[b.parent_id, b.child_id]::uuid[],
           (b.child_id = b.parent_id)
      FROM bom_components b

    UNION ALL

    SELECT w.root_id,
           b.child_id,
           w.path || b.child_id,
           (b.child_id = ANY(w.path))
      FROM walk w
      JOIN bom_components b ON b.parent_id = w.node_id
     WHERE NOT w.is_cycle
  )
  SELECT path
    INTO cycle_path
    FROM walk
   WHERE is_cycle
   LIMIT 1;

  IF cycle_path IS NOT NULL THEN
    RAISE EXCEPTION USING
      ERRCODE = '23514',
      MESSAGE = 'BOM cycle detected; transaction rejected',
      DETAIL = array_to_string(cycle_path, ' -> ');
  END IF;
END;
$$ LANGUAGE plpgsql;

-- Deferred validation checks the FINAL graph produced by a transaction.  This
-- is important for an ECO that atomically removes one edge and adds another:
-- temporary intermediate topology is allowed, cyclic final topology is not.
CREATE OR REPLACE FUNCTION bom_cycle_constraint_check()
RETURNS trigger AS $$
BEGIN
  -- A deferred row trigger is queued once per changed row. Validate the final
  -- graph only once per transaction; subsequent queued invocations can skip.
  IF COALESCE(current_setting('cpim_mes.bom_cycle_checked', true), '0') <> '1' THEN
    PERFORM assert_bom_acyclic();
    PERFORM set_config('cpim_mes.bom_cycle_checked', '1', true);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS bom_cycle_guard ON bom_components;
CREATE CONSTRAINT TRIGGER bom_cycle_guard
AFTER INSERT OR UPDATE ON bom_components
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION bom_cycle_constraint_check();

-- Replace the original fixed-100-pass implementation.  An acyclic directed
-- graph with N items has a longest simple path of at most N-1 edges.
CREATE OR REPLACE FUNCTION recompute_low_level_codes()
RETURNS void AS $$
DECLARE
  changed    int;
  iter       int := 0;
  item_count int := 0;
BEGIN
  -- Give a precise cycle error before mutating LLC values.
  PERFORM assert_bom_acyclic();

  SELECT COUNT(*) INTO item_count FROM items;
  UPDATE items SET low_level_code = 0;

  LOOP
    UPDATE items c
       SET low_level_code = sub.new_llc
      FROM (
        SELECT b.child_id AS id, MAX(p.low_level_code) + 1 AS new_llc
          FROM bom_components b
          JOIN items p ON p.id = b.parent_id
         GROUP BY b.child_id
      ) sub
     WHERE c.id = sub.id
       AND sub.new_llc > c.low_level_code;

    GET DIAGNOSTICS changed = ROW_COUNT;
    EXIT WHEN changed = 0;

    iter := iter + 1;
    IF iter > GREATEST(item_count, 1) THEN
      RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'LLC recompute did not converge; cyclic or invalid BOM';
    END IF;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Existing databases are validated immediately when this migration is applied.
-- If legacy cyclic data exists, migration intentionally aborts without changing
-- the database; the cycle must be corrected explicitly.
SELECT assert_bom_acyclic();
SELECT recompute_low_level_codes();

COMMIT;
