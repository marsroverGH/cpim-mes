-- Count stored values that carry binary float artefacts.
--
-- Before v0.42.1, quantities computed in Go were written as float64, so the
-- numeric columns without a fixed scale could receive values such as
-- 0.30000000000000004. Since v0.42.1 every computed quantity is rounded to
-- 9 decimal places (service.QuantityScale) before it is stored, so a value
-- with more than 9 decimal places can only be left over from an older release.
--
-- The query scans every numeric column without a fixed scale in the public
-- schema (columns such as numeric(18,6) cannot hold artefacts) and returns one
-- row per column that has at least one such value. No rows means no artefacts.
--
-- It only reads. Run it in a read-only session, preferably against a replica
-- or a restored backup, because every listed column is scanned in full:
--
--   PGOPTIONS='-c default_transaction_read_only=on -c statement_timeout=30min' \
--     psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/sql/check_quantity_float_artefacts.sql
--
-- See docs/QUANTITY_PRECISION.md for how to read and act on the result.
WITH unscaled AS (
    SELECT c.table_name, c.column_name
      FROM information_schema.columns c
      JOIN information_schema.tables t
        ON t.table_schema = c.table_schema AND t.table_name = c.table_name
     WHERE c.table_schema = 'public'
       AND t.table_type = 'BASE TABLE'
       AND c.data_type = 'numeric'
       AND c.numeric_scale IS NULL
), counted AS (
    SELECT u.table_name, u.column_name,
           query_to_xml(format(
               'SELECT count(*) AS n, max(scale(%1$I)) AS max_scale, min(%1$I)::text AS example
                  FROM public.%2$I WHERE scale(%1$I) > 9',
               u.column_name, u.table_name), false, true, '') AS x
      FROM unscaled u
)
SELECT table_name,
       column_name,
       (xpath('/row/n/text()', x))[1]::text::bigint         AS rows_over_9_decimals,
       (xpath('/row/max_scale/text()', x))[1]::text::int    AS max_scale,
       (xpath('/row/example/text()', x))[1]::text           AS example
  FROM counted
 WHERE (xpath('/row/n/text()', x))[1]::text::bigint > 0
 ORDER BY rows_over_9_decimals DESC, table_name, column_name;
