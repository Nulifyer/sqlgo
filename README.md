<div align="center">

# sqlgo

**A fast, keyboard-driven TUI SQL client for PostgreSQL, MySQL, SQL Server, SQLite,  flat files, and more.**

[![Release](https://img.shields.io/github/v/release/Nulifyer/sqlgo?style=flat-square)](https://github.com/Nulifyer/sqlgo/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/Nulifyer/sqlgo?style=flat-square&logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-informational?style=flat-square)](https://github.com/Nulifyer/sqlgo/releases)

</div>

## Why sqlgo?

Heavy GUI clients are slow to launch, hard to script around, and hostile over SSH. `psql` and `mysql` are great until you want a result pane you can actually scroll. sqlgo sits in the middle: a terminal-native workbench that starts in milliseconds, runs everywhere, and works the same against every engine it supports.

No mouse required. No Electron. One binary.

## Features

### 🗄️ Databases

| Engine | Driver | Notes |
|---|---|---|
| PostgreSQL | `jackc/pgx/v5` | aliases: CockroachDB, Supabase, Neon, YugabyteDB, TimescaleDB |
| MySQL | `go-sql-driver/mysql` | alias: MariaDB |
| SQL Server | `microsoft/go-mssqldb` | |
| SQLite | `mattn/go-sqlite3` | cgo, FTS5 |
| Oracle | `sijms/go-ora/v2` | pure Go |
| Firebird | `nakagami/firebirdsql` | pure Go, 2.5 / 3.x / 4.x |
| Turso / libSQL | (built-in hrana v3 client) | remote-only |
| Cloudflare D1 | (built-in REST client) | no transactions |
| Files | `mattn/go-sqlite3` + importer | CSV / TSV / JSONL loaded into SQLite (in-memory; spills to a temp file when total input exceeds `SQLGO_BYTE_CAP`) |

### 🪟 Workbench

- **Three-panel layout** -- Explorer (schema tree), Query (editor), Results (table). Toggle focus with `Alt+1/2/3`.
- **Fullscreen editor** -- `F11` maximizes the query pane.
- **Streaming results** -- rows land live with a running count; cancel mid-query with `Ctrl+C`.
- **Multi-connection** -- saved connections switch at runtime from the command menu.

### ⌨️ Editor

- Multi-line with **undo / redo** (`Ctrl+Z` / `Ctrl+Y`)
- **Find / replace** (`Ctrl+F`)
- **Autocomplete** on column names cached per-connection (`Ctrl+Space`)
- **Multi-cursor** (`Ctrl+Alt+Up/Down`)
- **SQL formatter** (`Alt+F`)
- **Bracketed paste** and standard cut / copy / paste

### 🌳 Schema Explorer

- Browse schemas, tables, and views
- `Enter` or `s` on a table drops a driver-aware `SELECT ... LIMIT 100` into the editor
- `R` refreshes the schema

### 📊 Results

- **Arrow-key cell nav**, `PgUp/PgDn`, `Home/End`
- **Sort** -- `s` cycles sort on the focused column
- **Filter** -- `/` for an inline row filter. Three syntaxes:
    - `foo` -- substring match across all columns
    - `col:foo` -- substring match against a specific column
    - `/regex/` -- regex match across all columns
- **Word-wrap** toggle -- `w`
- **Cell inspector** -- `Enter` opens an overlay for the focused cell
- **Clipboard** -- `y` copies cell, `Y` copies row, `Alt+A` copies the whole result set as TSV
- **Export** -- CSV, TSV, JSON, and Markdown. Format is chosen from the output path extension.

### ⚡ Power Features

- 🔐 **SSH tunneling** -- optional jump host per connection with password or key-file auth and TOFU host-key prompts. `ssh-agent` is not supported; supply a key file or password. See [docs/ssh-tunneling.md](docs/ssh-tunneling.md).
- 🔑 **OS keyring** -- passwords are stored in the system keychain when available (falls back to plain store with a warning)
- 🕘 **Query history** -- last 1000 queries per connection, FTS5-indexed, retrievable from the history overlay
- 🔎 **EXPLAIN overlay** -- run the current query through its engine's explain path (Postgres, MySQL, SQLite, SQL Server via `SHOWPLAN_XML`)
- 📂 **Open file** -- `o` opens a workspace file picker rooted at the current directory; honors a `.sqlgoignore` file for directory skips

---

## Install

### Linux / macOS

```sh
curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/install.sh | bash
```

Installs to `~/.local/bin/sqlgo`. Make sure that directory is on your `PATH`.

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\sqlgo\sqlgo.exe` and adds that directory to your user `PATH`.

### From source

```sh
CGO_ENABLED=1 go install -tags sqlite_fts5 github.com/Nulifyer/sqlgo/cmd/sqlgo@latest
```

Requires a C toolchain (gcc, clang, or `zig cc`). The `sqlite_fts5` tag enables the FTS5 module that powers query history search.

### Uninstall

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.sh | bash

# Windows
irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.ps1 | iex
```

Add `--purge` (Linux/macOS) or `-Purge` (Windows) to also delete saved connections and query history.

## Usage

Launch the TUI:

```sh
sqlgo
```

Open the command menu with `Ctrl+K` for global actions (connect, disconnect, history, quit). Focus-scoped actions (save, open, export, explain, set active DB) are direct binds on the panel that owns them -- see the table below. Press `F1` for an in-app help overlay.

### Keybindings

| Context | Key | Action |
|---|---|---|
| **Global** | `Ctrl+Q` | Quit |
| | `Ctrl+K` | Open command menu |
| | `Alt+1` / `Alt+2` / `Alt+3` | Focus Explorer / Query / Results |
| | `F1` | Help overlay |
| | `F8` | Key-debug overlay |
| **Query editor** | `F5` | Run query |
| | `F9` | EXPLAIN current query |
| | `Ctrl+C` | Cancel running query (copies selection when idle) |
| | `Ctrl+O` | Open SQL file |
| | `Ctrl+S` / `Alt+S` | Save tab / save as |
| | `Ctrl+R` | Rename tab |
| | `Alt+D` | Set active database for this tab (SSMS-style) |
| | `Ctrl+T` / `Ctrl+W` | New tab / close tab |
| | `Ctrl+PgUp` / `Ctrl+PgDn` | Cycle query tabs |
| | `F11` | Toggle fullscreen editor |
| | `Alt+F` | Format SQL |
| | `Ctrl+Z` / `Ctrl+Y` | Undo / Redo |
| | `Ctrl+Space` | Autocomplete |
| | `Ctrl+F` | Find / replace |
| | `Ctrl+G` | Go to line |
| | `Ctrl+A` / `Ctrl+X` / `Ctrl+V` | Select all / cut / paste |
| | `Ctrl+Alt+Up/Dn` | Add multi-cursor line |
| | `Alt+Up/Dn` | Move line up / down |
| | `Shift+Alt+Up/Dn` | Duplicate line up / down |
| | `Ctrl+D` | Select word under cursor |
| | `Ctrl+U` | Clear selection |
| | `Home` | Smart home (toggle indent / col 0) |
| | `Esc` | Collapse multi-cursor |
| | `Ctrl+Left` / `Ctrl+Right` | Word-jump |
| | `Ctrl+Backspace` / `Ctrl+Delete` | Delete word left / right |
| | `Shift+Arrow` / `Shift+Home/End` | Extend selection |
| | `Ctrl+Shift+Left` / `Ctrl+Shift+Right` | Extend selection by word |
| | `Ctrl+Shift+Home` / `Ctrl+Shift+End` | Extend selection to buffer start / end |
| | `Ctrl+L` | Clear editor |
| **Explorer** | `Enter` | SELECT for tables / views; open DDL for routines / triggers |
| | `s` | Generate `SELECT` for table / view |
| | `e` | Open DDL for view / routine / trigger |
| | `u` | Pin active database to cursor |
| | `R` | Refresh schema |
| **Results** | `Ctrl+E` | Export results |
| | `Arrows` / `PgUp/PgDn` / `Home/End` | Navigate cells |
| | `Enter` | Inspect cell |
| | `y` / `Y` / `Alt+A` | Copy cell / row / all (TSV) |
| | `s` | Cycle sort on column |
| | `/` | Filter rows |
| | `w` | Toggle word-wrap |
| | `Ctrl+PgUp` / `Ctrl+PgDn` | Cycle result-set tabs |
| | `Shift+double-click` | Copy row |
| **Cell inspector** | `Up`/`Dn`/`PgUp`/`PgDn` | Scroll |
| | `Home` / `End` | Scroll to top / bottom |
| | `y` | Copy cell |
| **EXPLAIN overlay** | `Up`/`Dn`/`PgUp`/`PgDn` | Move selection |
| | `Home` / `End` | First / last node |
| | `Space` | Toggle collapse node |
| | `r` | Toggle raw output |
| **Query history** | `Up`/`Dn`/`PgUp`/`PgDn` | Move selection |
| | `type` | Search filter |
| | `Enter` | Paste into editor |
| | `d` | Delete entry |
| | `X` | Clear all (two-press) |
| | `Tab` | Toggle scope (this conn / all) |
| **Connection form** | `Ctrl+T` | Test network |
| | `Ctrl+L` | Test auth |
| | `Ctrl+S` | Save |
| **Connection picker** | `a` / `e` / `x` | Add / edit / delete |
| | `K` | Unlink keyring entry |
| **Safety prompts** | confirm run: `y` / `n` / `Esc` / `Tab` / `Enter` | Run destructive DML/DDL guard |
| | SSH trust: `y` / `n` / `Esc` / `Enter` | TOFU host-key accept (Enter arms, then confirms) |
| **Query tabs** | `Double-click tab` | Rename |
| **Command menu** (`Ctrl+K`) | `c` / `x` | Connect / Disconnect |
| | `h` | Query history |
| | `q` | Quit |


## Scripting (non-TUI)

sqlgo also runs headless. When the first argument is a known verb, nothing in the TUI is loaded -- the binary behaves like `psql`/`mysql` for shell scripts and CI jobs.

| Verb | Purpose |
|---|---|
| `exec` | run SQL and print results (default: table on a tty, TSV on a pipe) |
| `export` | run SQL and write results to a file or stdout (default: CSV) |
| `conns` | manage saved connections (`list`, `show`, `add`, `set`, `rm`, `test`, `import`, `export`) |
| `history` | inspect query history (`list`, `search`, `clear`) |
| `version` | print sqlgo version (also `--version` / `-v`) |
| `completion` | print shell-completion script for `bash` / `zsh` / `fish` / `powershell` (alias `pwsh`) |

Common flags (shared by `exec` and `export`):

| Flag | Effect |
|---|---|
| `-c NAME` / `--conn NAME` | use a saved connection |
| `--dsn URL` | inline `scheme://user:pass@host:port/db?opt=val` |
| `-q SQL` / `--query SQL` | inline SQL |
| `-f PATH` / `--file PATH` | read SQL from a file (`-` for stdin) |
| `--format FMT` | `csv` / `tsv` / `json` / `jsonl` / `markdown` / `table` |
| `-o PATH` / `--output PATH` | output file (format inferred from extension unless `--format` set) |
| `--max-rows N` | stop after N rows (exit 5) |
| `--timeout DUR` | abort the query batch after DUR |
| `--allow-unsafe` | permit destructive DML/DDL (UPDATE/DELETE without WHERE, TRUNCATE, DROP) |
| `--continue-on-error` | keep running remaining statements on failure |
| `--record-history` | append to the history store (off by default for CLI) |
| `--password-stdin` | read the connection password from stdin |

Password precedence: `--password-stdin` > `$SQLGO_PASSWORD` > DSN / keyring.

DSN schemes: `postgres://`, `mysql://`, `mssql://` (or `sqlserver://`), `sqlite://`, `oracle://`, `firebird://`, `libsql://`, `d1://`. Postgres aliases (`cockroachdb`, `supabase`, `neon`, `yugabytedb`, `timescaledb`) and MySQL alias (`mariadb`) are also accepted as schemes and as driver names under `conns add --driver`.

### `conns` subcommands

| Subcommand | Flags |
|---|---|
| `list` \| `ls` | `--format FMT` |
| `show NAME` | -- |
| `add NAME` | `--driver NAME` (required), `--host`, `--port`, `--user`, `--database`, `--option k=v` (repeatable), `--password-stdin`, `--keyring=true\|false` (default true), `--force`, `--ssh-host`, `--ssh-port`, `--ssh-user`, `--ssh-key PATH`, `--ssh-password-stdin` |
| `set NAME` | same flags as `add`; upserts an existing connection. Only fields whose flag is supplied are overwritten |
| `rm NAME` | `--force` (suppress error if missing) |
| `test NAME` | `--timeout DUR` (default 10s), `--password-stdin` |
| `import` | `-i FILE` / `--input FILE` (default stdin) |
| `export` | `-o FILE` / `--output FILE` (default stdout) |

Passwords default to the OS keyring. When the keyring is unavailable the password is stored in plaintext in the store with a warning on stderr; pass `--keyring=false` to force plaintext. `--ssh-password-stdin` reads a second newline-delimited value from stdin when both `--password-stdin` and `--ssh-password-stdin` are set; use `$SQLGO_SSH_PASSWORD` as the env equivalent.

### `history` subcommands

| Subcommand | Flags |
|---|---|
| `list` \| `ls` | `-c NAME` / `--conn NAME`, `--limit N` (default 50), `--format FMT` |
| `search QUERY` | `-c NAME` / `--conn NAME`, `--limit N` (default 50), `--format FMT`; QUERY can appear before or after flags |
| `clear` | `-c NAME` / `--conn NAME` (scope to one connection), `--force` (required) |

`exec` and `export` do not record history unless `--record-history` is passed -- CLI-driven queries stay out of the ring by default.

Examples:

```sh
# Saved connection, auto-detected stdin, human-readable table output
sqlgo exec -c prod -q "select version()"

# Piped SQL file, CSV to a file
cat report.sql | sqlgo export -c prod -o report.csv

# Inline DSN, JSONL for downstream tools
sqlgo export --dsn "postgres://me@db.local:5432/app" -q "select * from users" --format jsonl

# Save a new connection (password read from stdin, stored in OS keyring)
echo -n "$PGPASSWORD" | sqlgo conns add prod --driver postgres \
    --host db.local --port 5432 --user me --database app --password-stdin

# Verify the connection is reachable
sqlgo conns test prod

# Backup / restore connections
sqlgo conns export -o conns.json
sqlgo conns import -i conns.json
```

`conns export` preserves keyring-backed passwords as placeholders, so it is useful for moving connection metadata but not for making a portable secret backup.

Exit codes:

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | usage / argument error |
| `2` | connection / store error |
| `3` | query error |
| `4` | refused: unsafe mutation without `--allow-unsafe` |
| `5` | `--max-rows` cap reached (partial output flushed) |

sqlgo never opens an implicit transaction. `BEGIN` / `COMMIT` / `ROLLBACK` are yours to type.

### Shell completion

`sqlgo completion <shell>` prints a completion script to stdout. Saved connection names are completed dynamically via a hidden `sqlgo __complete conns` helper.

```sh
# bash (session)
source <(sqlgo completion bash)
# bash (persistent)
sqlgo completion bash > ~/.local/share/bash-completion/completions/sqlgo

# zsh -- save as _sqlgo on your $fpath
sqlgo completion zsh > ~/.zfunc/_sqlgo

# fish
sqlgo completion fish > ~/.config/fish/completions/sqlgo.fish
```

```powershell
# PowerShell -- add to $PROFILE
sqlgo completion pwsh | Out-String | Invoke-Expression
```

## Storage

sqlgo keeps per-user state in the platform-native data directory:

| OS | Path |
|---|---|
| Linux | `$XDG_DATA_HOME/sqlgo` (default `~/.local/share/sqlgo`) |
| macOS | `~/Library/Application Support/sqlgo` |
| Windows | `%LocalAppData%\sqlgo` |

Contents:

- `sqlgo.db` -- SQLite store holding saved connections and query history (WAL mode)

Passwords go to the OS keyring when one is available (macOS Keychain, Windows Credential Manager, libsecret on Linux). SSH passwords are stored under their own keyring entry.

## Contributor Verification

FTS5-backed history search is part of the supported build, so contributor and CI verification should use the tagged test command:

```sh
go test -tags sqlite_fts5 ./...
```

### Environment Variables

| Variable | Default | Effect |
|---|---|---|
| `SQLGO_BYTE_CAP` | `2147483648` (2 GiB) | Max bytes buffered per result set; also the file-driver threshold above which input spills from in-memory SQLite to a temp file |
| `SQLGO_DEBUG` | unset | When set to `1`, panics dump a stack trace to `sqlgo-panic-<unix>.log` in the working directory |


## License

[MIT](LICENSE)
