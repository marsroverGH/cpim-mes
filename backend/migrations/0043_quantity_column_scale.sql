-- 0043 Quantity column scale
--
-- Fixes the main quantity columns at numeric(20,9). Since v0.42.1 the backend
-- rounds every quantity it computes to 9 decimal places (service.QuantityScale)
-- and, since v0.43.0, computes the main quantity paths in decimal. Releases up
-- to v0.42.0 could store binary float artefacts such as 0.30000000000000004 in
-- these unconstrained numeric columns; the type change rounds them to 9 places
-- (half away from zero) and prevents new ones.
--
-- Every ledger guard compares with a 0.000001 tolerance. Rounding moves each
-- value by at most 0.0000000005, so the invariants keep holding; the
-- reconciliation is asserted again at the end of this migration.
--
-- ALTER COLUMN TYPE cannot run while a view selects the column, so the views
-- that do are captured with pg_get_viewdef, dropped, and recreated unchanged
-- (with their comments) after the type change. The type change rewrites each
-- table under an ACCESS EXCLUSIVE lock: run the upgrade in a maintenance window.
--
-- Columns that are not quantities of material (ratios, percentages, minutes,
-- costs) and planning outputs (forecasts, schedules) keep their current type.
-- cycle_counts is also left as it is: its variance is a generated column over
-- counted_qty and expected_qty, which blocks the type change, and stocktake
-- counts are entered by people rather than computed.

CREATE TEMP TABLE quantity_scale_0043_columns (table_name text NOT NULL, column_name text NOT NULL) ON COMMIT DROP;
INSERT INTO quantity_scale_0043_columns (table_name, column_name) VALUES
    -- inventory ledger
    ('inventory_txns', 'quantity'),
    ('lot_movements', 'quantity'),
    ('lots', 'quantity'),
    -- item master and BOM
    ('items', 'safety_stock'),
    ('items', 'lot_size'),
    ('bom_components', 'quantity'),
    -- master production schedule
    ('mps_entries', 'planned'),
    ('mps_entries', 'released'),
    -- work orders
    ('work_orders', 'quantity'),
    ('work_orders', 'completed_qty'),
    ('work_orders', 'reported_progress_qty'),
    ('work_order_bom_snapshot_lines', 'quantity_per'),
    ('work_order_bom_snapshot_lines', 'required_qty'),
    ('work_order_completions', 'quantity'),
    ('wo_operations', 'completed_qty'),
    ('operation_logs', 'quantity'),
    -- purchasing
    ('purchase_orders', 'quantity'),
    ('purchase_orders', 'received_qty'),
    ('purchase_receipts', 'quantity'),
    -- sales orders
    ('sales_order_lines', 'quantity'),
    ('sales_order_lines', 'allocated_qty'),
    ('sales_order_lines', 'shipped_qty'),
    ('sales_order_lines', 'cancelled_qty'),
    ('sales_order_shipments', 'quantity'),
    ('sales_order_allocation_events', 'quantity');

DO $$
DECLARE
    col record;
    v record;
    too_large bigint;
    pending integer;
    created integer;
BEGIN
    -- Every listed column must exist and still be an unconstrained numeric.
    FOR col IN SELECT q.table_name, q.column_name, c.data_type, c.numeric_scale
                 FROM quantity_scale_0043_columns q
                 LEFT JOIN information_schema.columns c
                   ON c.table_schema = 'public' AND c.table_name = q.table_name AND c.column_name = q.column_name
    LOOP
        IF col.data_type IS DISTINCT FROM 'numeric' OR col.numeric_scale IS NOT NULL THEN
            RAISE EXCEPTION '0043: %.% is not an unconstrained numeric column (type %, scale %)',
                col.table_name, col.column_name, col.data_type, col.numeric_scale;
        END IF;
    END LOOP;

    -- numeric(20,9) holds values below 10^11; fail with the column name rather
    -- than a bare numeric overflow.
    FOR col IN SELECT table_name, column_name FROM quantity_scale_0043_columns ORDER BY table_name, column_name LOOP
        EXECUTE format('SELECT count(*) FROM public.%I WHERE abs(%I) >= 99999999999.9999999995',
                       col.table_name, col.column_name) INTO too_large;
        IF too_large > 0 THEN
            RAISE EXCEPTION '0043: %.% has % value(s) that do not fit numeric(20,9)',
                col.table_name, col.column_name, too_large;
        END IF;
    END LOOP;

    -- Capture and drop the views that select any listed column.
    CREATE TEMP TABLE quantity_scale_0043_views ON COMMIT DROP AS
    SELECT DISTINCT vc.oid, vc.relname AS view_name,
           pg_get_viewdef(vc.oid) AS definition,
           obj_description(vc.oid, 'pg_class') AS comment
      FROM quantity_scale_0043_columns q
      JOIN pg_class tc ON tc.relname = q.table_name AND tc.relnamespace = 'public'::regnamespace
      JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = q.column_name
      JOIN pg_depend d ON d.refobjid = tc.oid AND d.refobjsubid = a.attnum AND d.classid = 'pg_rewrite'::regclass
      JOIN pg_rewrite r ON r.oid = d.objid
      JOIN pg_class vc ON vc.oid = r.ev_class AND vc.oid <> tc.oid AND vc.relkind = 'v';

    -- A view built on top of one of these views would be dropped with it and
    -- not recreated; refuse instead of losing it silently.
    IF EXISTS (
        SELECT 1 FROM pg_depend d
          JOIN pg_rewrite r ON r.oid = d.objid
          JOIN pg_class dep ON dep.oid = r.ev_class
         WHERE d.classid = 'pg_rewrite'::regclass
           AND d.refobjid IN (SELECT oid FROM quantity_scale_0043_views)
           AND dep.oid NOT IN (SELECT oid FROM quantity_scale_0043_views)
           AND dep.oid <> d.refobjid
    ) THEN
        RAISE EXCEPTION '0043: a view depends on a view that must be recreated; extend this migration';
    END IF;

    FOR v IN SELECT view_name FROM quantity_scale_0043_views ORDER BY view_name LOOP
        EXECUTE format('DROP VIEW public.%I', v.view_name);
    END LOOP;

    -- Triggers whose UPDATE OF list or WHEN clause names a listed column also
    -- block the type change; capture and recreate them the same way, keeping
    -- their enabled/disabled state.
    CREATE TEMP TABLE quantity_scale_0043_triggers ON COMMIT DROP AS
    SELECT DISTINCT t.oid, t.tgname AS trigger_name, tc.relname AS table_name,
           pg_get_triggerdef(t.oid) AS definition, t.tgenabled AS enabled
      FROM quantity_scale_0043_columns q
      JOIN pg_class tc ON tc.relname = q.table_name AND tc.relnamespace = 'public'::regnamespace
      JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = q.column_name
      JOIN pg_depend d ON d.refobjid = tc.oid AND d.refobjsubid = a.attnum AND d.classid = 'pg_trigger'::regclass
      JOIN pg_trigger t ON t.oid = d.objid AND NOT t.tgisinternal;

    FOR v IN SELECT * FROM quantity_scale_0043_triggers ORDER BY table_name, trigger_name LOOP
        EXECUTE format('DROP TRIGGER %I ON public.%I', v.trigger_name, v.table_name);
    END LOOP;

    FOR col IN SELECT table_name, column_name FROM quantity_scale_0043_columns ORDER BY table_name, column_name LOOP
        EXECUTE format('ALTER TABLE public.%I ALTER COLUMN %I TYPE numeric(20,9)', col.table_name, col.column_name);
    END LOOP;

    FOR v IN SELECT * FROM quantity_scale_0043_triggers ORDER BY table_name, trigger_name LOOP
        EXECUTE v.definition;
        IF v.enabled = 'D' THEN
            EXECUTE format('ALTER TABLE public.%I DISABLE TRIGGER %I', v.table_name, v.trigger_name);
        ELSIF v.enabled <> 'O' THEN
            RAISE EXCEPTION '0043: trigger % on % has unsupported enabled state %', v.trigger_name, v.table_name, v.enabled;
        END IF;
    END LOOP;

    -- Recreate the views. One view may select from another captured view, so
    -- retry until every view exists or no progress is made.
    LOOP
        created := 0;
        FOR v IN SELECT * FROM quantity_scale_0043_views ORDER BY view_name LOOP
            IF to_regclass(format('public.%I', v.view_name)) IS NULL THEN
                BEGIN
                    EXECUTE format('CREATE VIEW public.%I AS %s', v.view_name, v.definition);
                    IF v.comment IS NOT NULL THEN
                        EXECUTE format('COMMENT ON VIEW public.%I IS %L', v.view_name, v.comment);
                    END IF;
                    created := created + 1;
                EXCEPTION WHEN undefined_table THEN
                    NULL; -- depends on a view not recreated yet
                END;
            END IF;
        END LOOP;
        SELECT count(*) INTO pending FROM quantity_scale_0043_views
         WHERE to_regclass(format('public.%I', view_name)) IS NULL;
        EXIT WHEN pending = 0;
        IF created = 0 THEN
            RAISE EXCEPTION '0043: could not recreate % view(s)', pending;
        END IF;
    END LOOP;

    -- The physical ledger must still reconcile after rounding.
    IF EXISTS (SELECT 1 FROM v_inventory_lot_reconciliation WHERE abs(difference) > 0.000001) THEN
        RAISE EXCEPTION '0043: inventory ledger and lot ledger no longer reconcile after rounding';
    END IF;
    IF EXISTS (SELECT 1 FROM lot_movements GROUP BY lot_id HAVING sum(quantity) < -0.000001) THEN
        RAISE EXCEPTION '0043: a lot balance became negative after rounding';
    END IF;
END $$;
