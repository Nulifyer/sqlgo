-- DML mix. Exercises INSERT ... SELECT, multi-row VALUES,
-- UPDATE with FROM/JOIN, DELETE with subquery, BEGIN/COMMIT.
BEGIN;

INSERT INTO audit_log (actor, action, payload, at)
VALUES
    ('system', 'rotate', '{"key":"a"}', CURRENT_TIMESTAMP),
    ('system', 'rotate', '{"key":"b"}', CURRENT_TIMESTAMP),
    ('alice',  'login',  NULL,          CURRENT_TIMESTAMP);

INSERT INTO archived_orders (order_id, user_id, total, placed_at)
SELECT o.id, o.user_id, o.total, o.placed_at
FROM orders AS o
WHERE o.placed_at < '2024-01-01'
    AND o.status IN ('paid', 'shipped', 'refunded');

UPDATE users
SET status = 'inactive',
    updated_at = CURRENT_TIMESTAMP
WHERE id IN (
    SELECT user_id
    FROM orders
    GROUP BY user_id
    HAVING MAX(placed_at) < '2024-01-01'
);

DELETE FROM sessions
WHERE expires_at < CURRENT_TIMESTAMP
    OR user_id IN (SELECT id FROM users WHERE status = 'banned');

COMMIT;
