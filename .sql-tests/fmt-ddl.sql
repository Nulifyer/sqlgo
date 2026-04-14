-- DDL: CREATE TABLE with constraints, indexes, CREATE VIEW with
-- a nontrivial body. Exercises multi-statement batch, inline
-- constraint keywords, CHECK + REFERENCES.
CREATE TABLE customers (
    id          BIGINT       NOT NULL PRIMARY KEY,
    email       VARCHAR(320) NOT NULL UNIQUE,
    name        VARCHAR(200) NOT NULL,
    tier        VARCHAR(20)  NOT NULL DEFAULT 'standard',
    region_id   INT              NULL REFERENCES regions (id),
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP        NULL,
    CONSTRAINT ck_tier CHECK (tier IN ('standard', 'pro', 'enterprise'))
);

CREATE INDEX ix_customers_region ON customers (region_id);
CREATE INDEX ix_customers_created ON customers (created_at DESC);

CREATE VIEW active_customers AS
SELECT c.id, c.email, c.name, c.tier, r.name AS region_name
FROM customers AS c
    LEFT JOIN regions AS r ON r.id = c.region_id
WHERE c.tier <> 'standard'
   OR EXISTS (
       SELECT 1
       FROM orders AS o
       WHERE o.customer_id = c.id
         AND o.placed_at >= CURRENT_TIMESTAMP - INTERVAL '90 days'
   );
