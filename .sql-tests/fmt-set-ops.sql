-- UNION / INTERSECT / EXCEPT chain with per-branch ORDER BY inside
-- parenthesized selects. Exercises set operators at depth zero and
-- the outer ORDER BY on the combined result.
(
    SELECT id, name, 'user' AS source
    FROM users
    WHERE status = 'active'
)
UNION ALL
(
    SELECT id, name, 'admin' AS source
    FROM admins
    WHERE disabled = false
)
UNION
(
    SELECT id, name, 'bot' AS source
    FROM service_accounts
    WHERE kind = 'bot'
)
EXCEPT
(
    SELECT id, name, 'user' AS source
    FROM users
    WHERE email LIKE '%@test.local'
)
ORDER BY source, name;
