-- Recursive + chained CTEs. Exercises: WITH, RECURSIVE,
-- multiple CTE bodies, column lists on the CTE header,
-- UNION ALL inside a CTE, outer SELECT joining a CTE.
WITH RECURSIVE org (id, parent_id, name, depth) AS (
    SELECT id, parent_id, name, 0
    FROM employees
    WHERE parent_id IS NULL
    UNION ALL
    SELECT e.id, e.parent_id, e.name, o.depth + 1
    FROM employees AS e
        INNER JOIN org AS o ON o.id = e.parent_id
),
top_spenders AS (
    SELECT user_id, SUM(total) AS spent
    FROM orders
    WHERE placed_at >= '2025-01-01'
    GROUP BY user_id
    HAVING SUM(total) > 1000
)
SELECT o.id, o.name, o.depth, COALESCE(ts.spent, 0) AS spent
FROM org AS o
    LEFT JOIN top_spenders AS ts ON ts.user_id = o.id
ORDER BY o.depth ASC, spent DESC;
