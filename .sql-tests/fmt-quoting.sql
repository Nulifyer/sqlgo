-- Quoting + literal stress. Exercises: double-quoted identifiers,
-- backtick identifiers, bracket identifiers, strings with embedded
-- quotes, dollar-quoted strings (Postgres), unicode/nvarchar
-- prefixes, hex + numeric literals, block + line comments mid-line.
SELECT
    "User Name"                         AS "display name",
    `orders`.`total`                     AS revenue, -- backtick ids
    [sales].[region name]                AS region,  /* bracket ids */
    'O''Brien'                           AS quoted_apostrophe,
    N'unicode: \u00e9'                   AS nstring,
    E'escape: \n\t'                      AS estring,
    $tag$ dollar-quoted body with 'quotes' and $$ inside $tag$ AS dq,
    0xDEADBEEF                           AS hex_lit,
    1.5e-3                               AS sci_lit,
    .25                                  AS dec_lit,
    x'4142'                              AS bytes_lit
FROM "Schema With Spaces"."Table With Spaces"
WHERE "display name" LIKE 'a%'          -- trailing comment
   OR region = 'north' /* inline */ AND revenue > 0;
