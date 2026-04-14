# Database Support Expansion Plan

Living plan. Update status boxes as work lands. Status keys: `[ ]` not started, `[~]` in progress, `[x]` done, `[-]` deferred.

## Goals

- Match sqlit-tui's engine coverage where feasible.
- Add **Sybase ASE (FreeTDS-compat)** and **CSV/TSV/JSONL file import**.
- **Prefer pure Go, but cgo ships in the default build** when the cgo driver is meaningfully faster or is the official/vendor-blessed option. No build tags -- every release binary has every supported driver.
- **No process forking, no shell spawning.** In-process cgo (linked C library) is fine; shelling out to `psql` / `sqlcmd` / `duckdb` CLI is not.
- Static-link C dependencies where upstream offers a static build. Dynamic linking is acceptable when it isn't (ODBC, Oracle instantclient). Document runtime dependencies in the README install section.
- Release pipeline must build with `CGO_ENABLED=1` on every target arch; update GoReleaser + CI accordingly.
- Minimize new dependencies; reuse the shared `db.OpenSQL` wrapper in [../internal/db/sqlconn.go](../internal/db/sqlconn.go).

## Cross-cutting prereqs

These land before or alongside Phase A so later adapters drop in cleanly.

- [x] **C1 Dynamic driver picker** -- connect form lists drivers from `db.Registered()` instead of hardcoding.
- [x] **C2 `Capabilities.SupportsTransactions`** -- some engines (BigQuery, Athena, D1) don't map cleanly to `BEGIN/COMMIT`. Gate the transaction UI affordances on this.
- [x] **C3 New `Dialect` tokens** -- `DialectOracle`, `DialectClickhouse`, `DialectSybase`, `DialectSnowflake`, `DialectBigQuery`. Formatter and safety classifier branch on these.
- [x] **C4 `LimitSyntaxFetchFirst`** variant for `OFFSET n ROWS FETCH NEXT m ROWS ONLY` (Oracle 12c+, Sybase 16+, DB2).
- [-] **C5 HTTP-backed driver base helper** -- deferred until the first HTTP consumer (D1 in Phase D) forces the shape. Extract the shim when BigQuery/Athena follow rather than designing speculatively.
- [-] **C6 Compose stack additions** -- deferred. Each adapter phase adds its own service to [../compose.yaml](../compose.yaml) alongside its integration tests, so the stack only carries engines with live test coverage. Sybase image choice resolves in Phase B; Oracle/ClickHouse in D/E.

## Phase A -- Free labels

Zero new adapter code. Pure labeling so the connect form exposes them.

- [x] **MariaDB** -- alias over the MySQL adapter.
- [x] **CockroachDB** -- alias over the Postgres adapter.
- [x] **Supabase** -- alias over Postgres.
- [x] **Neon** -- alias over Postgres.
- [x] **YugabyteDB** -- alias over Postgres.
- [x] **TimescaleDB** -- alias over Postgres.

## Phase B -- Sybase ASE (deferred)

User runs Sybase at work with FreeTDS -- highest-value addition in principle, but deferred due to driver risk.

- **Driver: `github.com/thda/tds`** -- pure Go, TDS 5.0. **RISKY**: last release v0.1.7 (Oct 2019), stalled ~6 years, ~60 stars, still beta-tagged. No actively-maintained pure-Go TDS 5.0 alternative exists (`go-mssqldb` is TDS 7+ for SQL Server). When revived: pin the commit, plan to fork if bugs surface.
- [-] Adapter at `internal/db/sybase/` using `github.com/thda/tds`.
- [-] Capabilities: `SchemaDepthSchemas`, `LimitSyntaxSelectTop`, `IdentifierQuote: '"'` after `set quoted_identifier on`, `Dialect: DialectSybase`, `SupportsTransactions: true`.
- [-] Schema query -- `sysobjects` / `syscolumns` / `sysusers`.
- [-] Integration tests behind `//go:build sybase_integration`.
- [-] Doc note in [adding-a-db-adapter.md](adding-a-db-adapter.md) if anything in the contract had to bend.

## Phase C -- File import (CSV/TSV/JSONL)

No new deps. In-memory SQLite via `github.com/mattn/go-sqlite3` with `file::memory:?cache=shared` (or a per-connection temp db).

- [x] `internal/db/fileimport/` package -- streaming loader that infers column types and streams rows into a table named after the file.
- [~] Connect form: new source kind "File". Currently `cfg.Database` accepts a `;`-separated list; multi-file picker UI still TODO.
- [x] Driver adapter is just the SQLite adapter pointed at the loaded in-memory db.
- [x] JSONL support same package; one row per line, columns = union of top-level keys.
- [ ] Reload on file change (optional, F5 in explorer).

## Phase D -- Straightforward pure-Go adapters

All drivers are pure Go; order by value.

- [x] **Oracle** -- `github.com/sijms/go-ora/v2`. **GOOD**: v2.9.0 (Jun 2025), active, ~939 stars, pure Go. `DialectOracle`, `LimitSyntaxFetchFirst`, quoted identifiers `"..."`.
- [x] **libSQL / Turso** -- hand-rolled hrana JSON-over-HTTP client (`net/http` only, pure Go). Covers the remote Turso use case; we don't need libSQL's local file format. ~1-2k LOC.
- [x] **Firebird** -- `github.com/nakagami/firebirdsql`. **OK**: active (21 tags, 894 commits), ~256 stars, pure Go, niche adoption. `LimitSyntaxFetchFirst`.
- [x] **Cloudflare D1** -- HTTP REST over stdlib `net/http` (C5 helper deferred until a second HTTP consumer). SQLite dialect. `SupportsTransactions: false` (single-statement HTTP).

## Phase E -- Heavy-dep adapters (build-tag gated)

Large dependency trees. Off by default; users opt in with a build tag.

- [ ] **ClickHouse** -- `github.com/ClickHouse/clickhouse-go/v2`. **GOOD**: official, v2.45.0, ~3.3k stars, pure Go (99%). Tag `clickhouse`.
- [ ] **Snowflake** -- `github.com/snowflakedb/gosnowflake`. **OK with caveat**: official, v1.19.1, active, but pulls in a Rust "minicore" via cgo by default. Requires `-tags minicore_disabled` + `CGO_ENABLED=0`. Document the build flag. Tag `snowflake`.
- [ ] **BigQuery** -- `cloud.google.com/go/bigquery`. **GOOD**: v1.76.0, Google-maintained, pure Go gRPC/REST. Tag `bigquery`. `SupportsTransactions: false`.
- [ ] **Spanner** -- `cloud.google.com/go/spanner`. **GOOD**: v1.90.0, Google-maintained, pure Go. Tag `spanner`.
- [ ] **Athena** -- `github.com/aws/aws-sdk-go-v2/service/athena`. **OK with caveat**: SDK is pure Go and active, but Athena has no `database/sql` driver -- must wrap `StartQueryExecution` + polling `GetQueryResults` by hand. Non-trivial adapter work. Tag `athena`. `SupportsTransactions: false`.
- [-] **Arrow Flight SQL** -- `(needs pure go solution)`. `github.com/apache/arrow-adbc/go/adbc/drivermgr` is a cgo wrapper around the C `adbc_driver_manager`. Pure-Go Flight SQL is achievable via `google.golang.org/grpc` + Arrow IPC but not off-the-shelf. Defer.

## Phase F -- cgo adapters (ship in default build)

Every release binary includes these. Pipeline work lands first. Scope intentionally limited to adapters where cgo is either the only option or a material speed win; we skip cgo Oracle/libSQL because pure-Go alternatives are already good enough.

- [ ] **F0 CI/release retool** -- flip `CGO_ENABLED=1`, add a C toolchain per target arch in the release workflow. Use native GitHub runners where available (`ubuntu-latest`, `ubuntu-24.04-arm`, `macos-13`, `macos-14`, `windows-latest`); use `zig cc` for windows/arm64. Update GoReleaser config.
- [ ] **F1 Runtime-dep docs** -- README install section lists what each adapter needs at runtime (SQLite: none; DuckDB: static-linked, none; ODBC: `unixODBC` on linux/macOS, built-in on Windows).
- [ ] **F2 Missing-lib guards** -- per-driver error translator + central runtime-dep registry. Detect dlopen/LoadLibrary failures on first `Ping`, surface "ODBC not installed -- install guide" style messages in the connect form. Pre-flight on driver selection where feasible.
- [x] **SQLite (mattn/go-sqlite3)** -- replaces `modernc.org/sqlite` as primary SQLite adapter. Faster on big imports (multi-GB CSV/JSONL ingest is a real use case). Also speeds up Phase C file-import. SQLite amalgamation compiles cleanly with any C toolchain, so cross-compile is easy.
- [ ] **DuckDB** -- `github.com/marcboeker/go-duckdb`, links `libduckdb` static lib. No pure-Go alternative exists; cgo is the only path. Upstream ships prebuilt static libs for all 6 targets.
- [ ] **ODBC bridge** -- `github.com/alexbrainman/odbc`. One adapter unlocks ~20 legacy/enterprise engines (Teradata, Vertica, Informix, DB2, SAP HANA, Netezza, Sybase IQ, Progress, etc.) with no pure-Go drivers.

## Deferred

- [-] **Arrow Flight SQL** -- no off-the-shelf pure-Go driver, ADBC drivermgr is cgo but also adds process-level driver loading. Best path is hand-rolled `google.golang.org/grpc` + Arrow IPC. Defer until there's demand.

## Explicitly skipped (acceptable pure-Go alternative exists)

- **`godror` (Oracle cgo)** -- `sijms/go-ora/v2` is pure Go, actively maintained, and GOOD-rated. Not worth the cross-compile cost for a marginal "official driver" badge.
- **`go-libsql` (libSQL cgo)** -- pulls in a Rust static lib, worst cross-compile story of the bunch. Hand-rolled hrana HTTP client in Phase D covers the actual Turso use case without the Rust dependency.

## Sequencing

```
C1-C6  ->  A  ->  B  ->  C  ->  D  ->  E  ->  F
```

Each phase finishes with: `go test ./...`, `go vet ./...`, manual smoke against dev compose stack, then summary + wait for user testing before the next phase starts.
