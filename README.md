# SQLGo

`SQLGo` is a native Go SQL TUI for modern desktop terminals, modeled after `sqlit` and aimed at replacing a large part of day-to-day SSMS usage while remaining cross-platform.

## Goals

- Native Go binary for Windows, macOS, and Linux
- Reliable boxed terminal UI built with `tview` and `tcell`
- Work-grade support for:
  - SQL Server
  - Azure SQL
  - Snowflake
  - SQLite
  - PostgreSQL
  - MySQL
  - Sybase ASE
- Large-result handling with streaming and bounded memory
- Safe execution of write queries with transaction guards
- CSV extraction with correct quoting and streaming output

## Dependency policy

When we add or upgrade a package, we prefer the latest stable tagged version available at the time of change.

Current direct dependencies:

- `github.com/rivo/tview` `v0.42.0`
- `github.com/gdamore/tcell/v2` `v2.13.8`
- `github.com/microsoft/go-mssqldb` `v1.9.8`
- `github.com/jackc/pgx/v5` `v5.8.0`
- `github.com/go-sql-driver/mysql` `v1.9.3`
- `modernc.org/sqlite` `v1.46.1`
- `github.com/snowflakedb/gosnowflake/v2` `v2.0.0`
- `github.com/thda/tds` `v0.1.7` for Sybase, currently isolated as experimental because the pure-Go ecosystem here is weaker than the other providers

## Current state

This repo currently contains:

- a runnable `tview`/`tcell` application shell
- an initial provider registry covering the planned database targets
- architecture notes for the next implementation phases

## Next steps

1. Add connection profile persistence and secret storage abstraction.
2. Add metadata explorer adapters, starting with SQL Server/Azure SQL.
3. Build the first query editor and results viewport.
4. Add streaming result caching and CSV export pipeline.
