-- Top-selling products last quarter by region.
/* Exercises: keywords, strings, numbers, operators,
   line + block comments, functions, plain + dotted + bracketed tables. */
SELECT TOP 50
    r.region_name,
    p.sku,
    p.name                              AS product,
    COUNT(DISTINCT o.order_id)          AS orders,
    SUM(oi.qty * oi.unit_price)         AS revenue,
    AVG(oi.unit_price)                  AS avg_price,
    CAST(SUM(oi.qty) AS DECIMAL(18, 2)) AS units
FROM dbo.orders AS o
    INNER JOIN dbo.order_items      AS oi ON oi.order_id = o.order_id
    INNER JOIN products             AS p  ON p.sku       = oi.sku
    LEFT  JOIN [sales].[regions]    AS r  ON r.region_id = o.region_id
    LEFT  JOIN analytics.fx_rates   AS fx ON fx.ccy      = o.currency
WHERE o.placed_at >= '2026-01-01'
    AND o.placed_at <  '2026-04-01'
    AND o.status IN ('paid', 'shipped')
    AND oi.unit_price BETWEEN 0.01 AND 9999.99
    AND p.name LIKE N'%pro%'
    AND r.region_name IS NOT NULL
GROUP BY r.region_name, p.sku, p.name
HAVING SUM(oi.qty * oi.unit_price) > 1000
ORDER BY revenue DESC, product ASC;
