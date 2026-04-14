-- Window functions + CASE + nested function calls. Exercises:
-- OVER(...) with PARTITION BY + ORDER BY + frame, ROW_NUMBER,
-- RANK, LAG/LEAD, CASE WHEN chains, NULLIF/COALESCE nesting.
SELECT
    u.id,
    u.name,
    o.placed_at,
    o.total,
    ROW_NUMBER() OVER (PARTITION BY u.id ORDER BY o.placed_at DESC) AS order_rank,
    RANK()       OVER (ORDER BY o.total DESC)                        AS total_rank,
    SUM(o.total) OVER (
        PARTITION BY u.id
        ORDER BY o.placed_at
        ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
    ) AS running_total,
    LAG(o.total, 1, 0)  OVER (PARTITION BY u.id ORDER BY o.placed_at) AS prev_total,
    LEAD(o.total, 1, 0) OVER (PARTITION BY u.id ORDER BY o.placed_at) AS next_total,
    CASE
        WHEN o.total > 1000 THEN 'large'
        WHEN o.total > 100  THEN 'medium'
        WHEN o.total > 0    THEN 'small'
        ELSE 'zero'
    END AS bucket,
    COALESCE(NULLIF(o.note, ''), '(none)') AS note_display
FROM users AS u
    INNER JOIN orders AS o ON o.user_id = u.id
WHERE o.status <> 'cancelled'
ORDER BY u.id, o.placed_at;
