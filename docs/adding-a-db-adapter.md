# Adding a Database Adapter

This guide covers adding a new engine to sqlgo. Existing adapters
under [internal/db/](../internal/db/) are the reference examples:
[sqlite](../internal/db/sqlite/sqlite.go) (simplest, `database/sql`
with a PRAGMA escape hatch), [mysql](../internal/db/mysql/mysql.go),
[postgres](../internal/db/postgres/postgres.go),
[mssql](../internal/db/mssql/mssql.go).

## What you implement

Every adapter is a package under [internal/db/](../internal/db/) that:

1. Imports the engine's `database/sql` driver for side effects.
2. Exports a `db.Profile` (dialect: capabilities, schema queries,
   definition fetcher, explain runner).
3. Exports a `db.Transport` (wire driver: `database/sql` driver name
   + `BuildDSN` or custom `Open`).
4. Implements [db.Driver](../internal/db/db.go) (`Name`,
   `Capabilities`, `Open`) as a thin preset that calls
   `db.OpenWith(ctx, Profile, Transport, cfg)`.
5. Registers all three in `init()` via `db.RegisterProfile`,
   `db.RegisterTransport`, and `db.Register`.

**Profile** is the dialect brain -- portable across wire transports.
The same ASE profile works over TDS today and could work over ODBC
tomorrow. **Transport** is the wire half -- one transport can back
many profiles (TDS -> mssql + sybase).

The preset `Driver` is the backward-compatible entry point that pairs
a default profile with a default transport. The "Other..." connection
flow in the TUI lets users pick profile and transport independently.

If the engine speaks `database/sql`, you almost never need to
implement `db.Conn` yourself. The shared wrapper handles streaming
`Rows`, `Schema`, `Columns`, `Ping`, `Exec`, `Close`,
`NextResultSet`, and the `[]byte -> string` conversion.

## Skeleton

```go
// Package foo registers the foo driver. Import for side effects.
package foo

import (
    "context"

    _ "github.com/example/foo-driver"

    "github.com/Nulifyer/sqlgo/internal/db"
    "github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "foo"

var Profile = db.Profile{
    Name: driverName,
    Capabilities: db.Capabilities{
        SchemaDepth:          db.SchemaDepthSchemas,
        LimitSyntax:          db.LimitSyntaxLimit,
        IdentifierQuote:      '"',
        SupportsCancel:       true,
        SupportsTLS:          true,
        ExplainFormat:        db.ExplainFormatNone,
        Dialect:              sqltok.DialectPostgres,
        SupportsTransactions: true,
    },
    SchemaQuery:  schemaQuery,
    ColumnsQuery: columnsQuery,
}

var FooTransport = db.Transport{
    Name:          "foo",
    SQLDriverName: "foo",
    DefaultPort:   5432,
    SupportsTLS:   true,
    BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
    return db.OpenWith(ctx, Profile, FooTransport, cfg)
}

func init() {
    db.RegisterProfile(Profile)
    db.RegisterTransport(FooTransport)
    db.Register(preset{})
}
```

## Registering the adapter

Blank-import the package from every entry point that needs it:

- [internal/tui/tui.go](../internal/tui/tui.go)

```go
_ "github.com/Nulifyer/sqlgo/internal/db/foo"
```

`db.Register`, `db.RegisterProfile`, and `db.RegisterTransport` all
panic on duplicate names, so each name must be unique within its
registry.

## SchemaQuery contract

`SchemaQuery` must return four columns in this order:

| col | type | meaning |
|-----|------|---------|
| `schema` | string | schema/namespace. Flat engines synthesize one (e.g. SQLite returns `'main'`). |
| `name` | string | table or view name. |
| `is_view` | int | `1` for views, `0` for base tables. |
| `is_system` | int | `1` for engine-internal catalogs. Drives the explorer's Sys bucket. |

The explorer splits rows with `is_system=1` into a top-level `Sys`
pseudo-schema next to user schemas. User schemas never see system
objects even when they physically live in a user-accessible schema
(e.g. MSSQL's `spt_*` tables in `dbo`).

### System-object detection, by engine

Each engine identifies internal objects differently. Pick whichever
covers *all* engine-shipped objects, not just the ones in obviously-
named schemas.

- **Postgres / MySQL**: filter by schema name list
  (`pg_catalog`, `information_schema`; `mysql`, `performance_schema`,
  `sys`, `information_schema`).
- **SQLite**: name prefix, `name LIKE 'sqlite_%'`.
- **MSSQL**: `sys.objects.is_ms_shipped = 1` OR schema IN
  (`sys`, `INFORMATION_SCHEMA`). The `is_ms_shipped` flag is what
  routes `spt_*` and `MSreplication_options` (physically in `dbo`)
  into Sys. Driving off `INFORMATION_SCHEMA.TABLES` misses objects
  in the `sys` schema entirely.

## ColumnsQuery vs ColumnsBuilder

For engines that accept positional bind values:

```go
ColumnsQuery: `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position;
`,
```

The shared wrapper passes `(TableRef.Schema, TableRef.Name)` as
positional args. Placeholder style is engine-specific
(`$1`/`$2` Postgres, `?`/`?` MySQL/SQLite, `@p1`/`@p2` MSSQL).

For engines where the column lookup can't take bind values (SQLite
`PRAGMA table_info`), use `ColumnsBuilder` instead. It takes
precedence over `ColumnsQuery` when both are set.

```go
ColumnsBuilder: func(t db.TableRef) (string, []any) {
    q := "SELECT name, type FROM pragma_table_info(" +
        quoteLiteral(t.Name) + ");"
    return q, nil
},
```

Builder inputs come from `sqlite_master`, not the user. Still escape
the literal defensively -- see `quoteSQLiteLiteral` in
[sqlite.go](../internal/db/sqlite/sqlite.go).

## Capabilities fields

Every field is read by the TUI; no adapter-specific branching lives
outside the adapter.

- **`SchemaDepth`** -- `SchemaDepthFlat` hides the schema layer in
  the explorer (SQLite). `SchemaDepthSchemas` groups tables/views
  under a schema node (everyone else).
- **`LimitSyntax`** -- `LimitSyntaxLimit` for `... LIMIT N`,
  `LimitSyntaxSelectTop` for MSSQL's `SELECT TOP N ...`.
- **`IdentifierQuote`** -- opening quote char: `'['` MSSQL,
  `` '`' `` MySQL, `'"'` ANSI. The closing character is derived
  (`[` -> `]`, else same as opening).
- **`SupportsCancel`** -- `true` if the driver honors
  `context.Cancel` on in-flight queries at the network layer.
- **`SupportsTLS`** -- drives whether the connection form shows TLS
  options. Set when `cfg.Options` understands TLS knobs.
- **`ExplainFormat`** -- selects the TUI's EXPLAIN renderer.
  `ExplainFormatNone` hides the feature. Existing shapes:
  `ExplainFormatPostgresJSON`, `ExplainFormatMySQLJSON`,
  `ExplainFormatSQLiteRows`, `ExplainFormatMSSQLXML`. Engines whose
  EXPLAIN flow can't be expressed as a one-shot wrapper (MSSQL needs
  `SET SHOWPLAN_XML ON` on a pinned `*sql.Conn` for the target
  query to return the plan instead of executing) supply a custom
  `SQLOptions.ExplainRunner` that runs the full flow and returns the
  raw plan rows. The TUI still dispatches parsing via `ExplainFormat`.
- **`Dialect`** -- which keyword overlay autocomplete should suggest.
  One of `sqltok.DialectMSSQL`, `DialectMySQL`, `DialectPostgres`,
  `DialectSQLite`. Keeps `TOP` out of Postgres suggestions and
  `RETURNING` out of MSSQL. Unset (zero) falls back to the full
  cross-engine set, which is almost never what you want.

## buildDSN

Convert `db.Config` to the engine's DSN. Conventions:

- Default `Host` to `localhost` and `Port` to the engine's default
  (`5432` Postgres, `3306` MySQL, `1433` MSSQL).
- Merge `cfg.Options` into the DSN last so user overrides win.
- Use the driver's own config builder when it has one
  (`gomysql.Config` for MySQL, `net/url` for MSSQL/Postgres-style
  URLs) so escaping is handled correctly.
- Keep `buildDSN` pure and exported-via-test only -- every adapter
  has table-driven tests over it (see
  [sqlite_test.go](../internal/db/sqlite/sqlite_test.go)).

## Testing

Hermetic tests (no network) go next to the adapter:

- Unit tests for `buildDSN` (table-driven, `t.Parallel()`).
- If the engine has an in-process mode (SQLite `:memory:`), use it
  to cover open -> schema -> columns -> query end-to-end.
- Integration tests that require a live server go behind a build
  tag (see
  [postgres_integration_test.go](../internal/db/postgres/postgres_integration_test.go))
  so `go test ./...` stays green on a clean checkout.

## Checklist

- [ ] Package under `internal/db/<name>/`.
- [ ] Exported `Profile` with all capabilities fields set.
- [ ] Exported `Transport` with `BuildDSN` (or `Open` for custom).
- [ ] Preset `Driver` delegates to `db.OpenWith`.
- [ ] `init()` calls `db.RegisterProfile`, `db.RegisterTransport`,
      `db.Register`.
- [ ] `Capabilities.Dialect` set so autocomplete uses the right
      keyword overlay.
- [ ] `SchemaQuery` returns the 4-column contract.
- [ ] System-object detection covers engine-shipped objects in user
      schemas (not only objects in obviously-named system schemas).
- [ ] `ColumnsQuery` (or `ColumnsBuilder`) returns
      `(name, type_name)` ordered by position.
- [ ] `buildDSN` merges `cfg.Options`, defaults host/port.
- [ ] Blank import added to `internal/tui/tui.go`.
- [ ] `buildDSN` + in-memory round-trip tests pass under
      `go test ./internal/db/<name>/...`.
