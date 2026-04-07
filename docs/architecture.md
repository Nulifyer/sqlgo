# Architecture Notes

## UI stack

- `tview` for layout and widgets
- `tcell` for terminal events, mouse support, colors, and custom drawing

This project prefers deterministic cell-based layouts over string-composed styling systems because the app is table-heavy and must preserve borders under resize and wrapping.

## Package layout

- `cmd/sqlgo`
  - application entrypoint
- `internal/app`
  - startup wiring and high-level orchestration
- `internal/ui`
  - screens, dialogs, grids, focus management, keymaps
- `internal/db`
  - provider definitions, capability registry, connections, execution, export

## Provider policy

Preferred rule: use pure-Go drivers whenever a reasonable stable option exists so we can ship native binaries for Windows, macOS, and Linux without a CGO requirement.

Initial provider set:

- SQL Server via `github.com/microsoft/go-mssqldb`
- Azure SQL via `github.com/microsoft/go-mssqldb/azuread`
- PostgreSQL via `github.com/jackc/pgx/v5/stdlib`
- MySQL via `github.com/go-sql-driver/mysql`
- SQLite via `modernc.org/sqlite`
- Snowflake via `github.com/snowflakedb/gosnowflake/v2`
- Sybase ASE via `github.com/thda/tds` as an experimental pure-Go adapter

## Version policy

Use the latest stable tagged version we can for direct dependencies at the time they are introduced or upgraded. For pre-v1 modules such as `tview`, that means using the latest tagged release rather than a pseudo-version from `main` unless there is a known blocker.

Before each dependency addition or upgrade:

1. Verify the latest tagged stable release in current package docs or official repo releases.
2. Prefer tagged releases over pseudo-versions.
3. Treat stale or weakly maintained packages as isolated adapters, not central abstractions.

## Immediate build milestones

1. Shell
   - provider explorer
   - editor pane
   - results pane
   - status bar
2. Connection profiles
   - persisted profiles
   - secret storage abstraction
3. Query runner
   - async execution
   - cancelation
   - transaction guard flow
4. Results viewport
   - streaming cache
   - sticky headers
   - horizontal scroll
5. Export
   - streaming CSV
   - proper quoting
   - progress and cancelation
