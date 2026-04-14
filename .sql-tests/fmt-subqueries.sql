-- Deeply nested subqueries: scalar subquery in SELECT list,
-- correlated EXISTS, derived table in FROM, IN (subquery),
-- ANY/ALL comparison. All parenthesized contexts should bump
-- their clause indent.
SELECT
    u.id,
    u.name,
    (
        SELECT COUNT(*)
        FROM orders AS o
        WHERE o.user_id = u.id
          AND o.status = 'paid'
    ) AS paid_orders,
    (
        SELECT MAX(o.placed_at)
        FROM orders AS o
        WHERE o.user_id = u.id
    ) AS last_order_at
FROM users AS u
    INNER JOIN (
        SELECT region_id, COUNT(*) AS user_count
        FROM users
        GROUP BY region_id
        HAVING COUNT(*) > 10
    ) AS rc ON rc.region_id = u.region_id
WHERE EXISTS (
        SELECT 1
        FROM orders AS o
        WHERE o.user_id = u.id
          AND o.total > (SELECT AVG(total) FROM orders)
    )
    AND u.id IN (SELECT user_id FROM verified_emails)
    AND u.signup_score >= ALL (
        SELECT min_score
        FROM tier_requirements
        WHERE tier = u.tier
    )
ORDER BY paid_orders DESC;
