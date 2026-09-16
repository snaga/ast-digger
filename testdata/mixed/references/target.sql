-- target.sql defines helperFn CTE
WITH helperFn AS (
    SELECT 'helper' AS val
)
SELECT * FROM dummy;
