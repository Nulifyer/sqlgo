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
- **Streaming results** -- rows land live with a running count; cancel mid-query from Results with `Ctrl+C`.
- **Multi-connection** -- saved connections switch at runtime from the command menu.

### ⌨️ Editor

- Multi-line with **undo / redo** (`Ctrl+Z` / `Ctrl+Y`)
- **Find / replace** (`Ctrl+F`)
- **Autocomplete** on column names cached per-connection (`Ctrl+␣`)
- **Multi-cursor** (`Ctrl+Alt+↑/↓`)
- **SQL formatter** (`Alt+F`)
- **Bracketed paste** and standard cut / copy / paste

### 🌳 Schema Explorer

- Browse databases, schemas, tables, views, routines, triggers, and table columns
- `↵` or `s` on a table drops a driver-aware `SELECT ... LIMIT 100` into the editor
- `␣` expands databases, schemas, folders, and table columns
- `y` copies a qualified object name
- `R` refreshes the schema or selected database

### 📊 Results

- **`↑/↓/←/→` cell nav**, `PgUp/PgDn`, `Home/End`
- **Sort** -- `s` cycles sort on the focused column
- **Filter** -- `Ctrl+F` opens an inline row filter. Three syntaxes:
    - `foo` -- substring match across all columns
    - `col:foo` -- substring match against a specific column
    - `/regex/` -- regex match across all columns
- **Word-wrap** toggle -- `w`
- **Cell inspector** -- `↵` opens an overlay for the focused cell
- **Clipboard** -- `y` copies cell, `Y` copies row, `Alt+A` copies all as TSV, `Alt+Shift+A` copies all as Markdown
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

Requires Go 1.27.3 or newer and a C toolchain (gcc, clang, or `zig cc`). The
`sqlite_fts5` tag enables the FTS5 module that powers query history search.
macOS binaries require macOS 13 Ventura or later.

### Update

For a release binary installed with the scripts above or downloaded directly:

```sh
sqlgo update --check  # Check for a newer stable release
sqlgo update          # Download, verify, and install it
```

The updater replaces the executable you are running, follows symlinks, and
verifies the release archive against `checksums.txt` before replacement. Saved
connections and query history are unaffected. Source and development builds
must use their original build or `go install` command. Package-manager installs
should use their package manager. On Windows, an in-use binary can remain as
`sqlgo.exe.old` until a later update removes it.

The TUI checks for updates in the background at most once per day using a local
cache. A notice appears in the footer when space permits. Press `F1` for the
installed version and update instructions. Checks have a five-second timeout,
and failures do not interrupt work. Exit the TUI before running `sqlgo update`.
Set `SQLGO_UPDATE_CHECK=0` to disable automatic checks. Non-TUI commands never
check automatically, and `sqlgo update --check` works with this setting disabled.

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
| | `F11` | Toggle fullscreen editor |
| **Help overlay** | `↑` / `↓` / `PgUp` / `PgDn` | Scroll |
| | `Home` / `End` | Scroll to top / bottom |
| | `F1` / `Esc` | Close |
| **Key debug** (`F8`) | any key / mouse | Verify matching bind in the checklist |
| | `Ctrl+R` | Reset checklist |
| | `F8` | Close |
| | `Ctrl+Q` / `F1` | Reserved globals shown there, but not capturable inside the overlay |
| **Query tabs** | `Ctrl+T` / `Ctrl+W` | New tab / close tab |
| | `Ctrl+S` | Save current tab |
| | `Ctrl+R` | Rename current tab |
| | `Ctrl+PgUp` / `Ctrl+PgDn` | Previous / next tab |
| | `Left-click tab` | Switch tab |
| | `Double-click tab` | Rename tab |
| | `Middle-click tab` | Close tab |
| **Query editor** | `F5` | Run query |
| | `F9` | EXPLAIN current query |
| | `Ctrl+O` | Open SQL file |
| | `Ctrl+S` / `Alt+S` | Save tab / save as |
| | `Alt+D` | Set active database for this tab (SSMS-style) |
| | `Alt+F` | Format SQL |
| | `Ctrl+␣` | Autocomplete |
| | `Ctrl+F` | Find / replace |
| | `F3` / `Shift+F3` | Next / previous match from the last search |
| | `Ctrl+G` | Go to line |
| | `Ctrl+L` | Clear editor |
| | `Ctrl+Z` / `Ctrl+Y` | Undo / Redo |
| | `Ctrl+A` / `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Select all / copy / cut / paste |
| | `⇥` / `⇤` | Indent / dedent |
| | `Ctrl+Alt+↑ / ↓` | Add multi-cursor line |
| | `Alt+↑ / ↓` | Move line up / down |
| | `Shift+Alt+↑ / ↓` | Duplicate line up / down |
| | `Esc` | Collapse extra cursors and clear selection |
| | `Ctrl+D` | Select word; repeat to add the next occurrence |
| | `Ctrl+U` / `Alt+U` | Undo / redo cursor and selection movement |
| | `Alt+/` / `Ctrl+/` | Toggle SQL line comments (`Ctrl+_` also works) |
| | `(` / `[` / `{` / quotes | Surround selected text |
| | `Home` | Smart home (toggle indent / col 0) |
| | `↑` / `↓` / `←` / `→` / `End` | Move caret |
| | `PgUp` / `PgDn` | Move one editor page; add Shift to select |
| | `↵` | New line, carrying leading indentation |
| | `Ctrl+←` / `Ctrl+→` | Word-jump |
| | `Ctrl+Home` / `Ctrl+End` | Jump to buffer start / end |
| | `Ctrl+Backspace` / `Ctrl+Delete` | Delete word left / right |
| | `Shift+↑` / `Shift+↓` / `Shift+←` / `Shift+→` / `Shift+Home` / `Shift+End` | Extend selection |
| | `Ctrl+Shift+←` / `Ctrl+Shift+→` | Extend selection by word |
| | `Ctrl+Shift+Home` / `Ctrl+Shift+End` | Extend selection to buffer start / end |
| **Find / replace** | `type` | Edit active field |
| | `↵` | Next match or replace current |
| | `⇥` | Toggle Find / Replace field |
| | `⇤` | Previous match |
| | `Ctrl+R` | Replace all |
| | `Alt+C` / `Alt+W` | Toggle match case / whole word |
| | `Esc` | Close |
| **Go to** | `type line[:col]` | Edit target |
| | `↵` | Jump |
| | `Esc` | Cancel |
| **Rename tab** | `type name` | Edit title |
| | `↵` | Save |
| | `Esc` | Cancel |
| **Explorer** | `Ctrl+F` | Search object names |
| | `Esc` | Close search |
| | `↑` / `↓` / `PgUp` / `PgDn` | Move cursor |
| | `↵` | SELECT table / view; expand folders; edit DDL for routines / triggers |
| | `␣` | Expand database / schema / group / table columns |
| | `s` | Generate `SELECT` for table / view |
| | `e` | Open DDL for view / routine / trigger |
| | `y` | Copy qualified object name |
| | `u` | Pin active database to cursor |
| | `R` | Refresh schema / database |
| **Active database picker** | `type` | Filter databases |
| | `↑` / `↓` | Move |
| | `↵` | Use selected database |
| | `Esc` | Cancel |
| **Results** | `Ctrl+C` | Cancel running query |
| | `Ctrl+F` | Filter rows |
| | `Ctrl+E` | Export results |
| | `↑` / `↓` / `←` / `→` / `PgUp` / `PgDn` / `Home` / `End` | Navigate cells |
| | `↵` | Inspect cell |
| | `y` / `Y` / `Alt+A` / `Alt+Shift+A` | Copy cell / row / all TSV / all Markdown |
| | `s` | Cycle sort on column |
| | `w` | Toggle word-wrap |
| | `Left-click result tab` | Switch result set |
| | `Ctrl+PgUp` / `Ctrl+PgDn` | Cycle result-set tabs |
| | `Shift+double-click` | Copy row |
| **Results error view** | `↑` / `↓` / `PgUp` / `PgDn` | Scroll |
| | `Home` / `End` | Scroll to top / bottom |
| | `y` / `Y` / `Alt+A` | Copy error text |
| **Results filter** | `type` | Edit filter |
| | `↵` | Keep filtered results |
| | `Esc` | Clear filter and close |
| **Cell inspector** | `↑` / `↓` / `PgUp` / `PgDn` | Scroll |
| | `Home` / `End` | Scroll to top / bottom |
| | `y` | Copy cell |
| | `Esc` | Close |
| **EXPLAIN overlay** | `↑` / `↓` / `PgUp` / `PgDn` | Move selection |
| | `Home` / `End` | First / last node |
| | `␣` | Toggle collapse node |
| | `r` | Toggle raw output |
| | `Esc` | Close |
| **Open SQL** | `⇥` / `⇤` | Next / previous field |
| | `↑` / `↓` / `PgUp` / `PgDn` | Move file list |
| | `␣` | Mark file for multi-open |
| | `↵` | Descend / pick / open |
| | `Esc` | Cancel |
| **Save As** | `⇥` / `⇤` | Next / previous field |
| | `↑` / `↓` | Move list or cycle extension |
| | `↵` | Descend / pick / save / overwrite |
| | `Esc` | Cancel |
| **Export results** | `⇥` / `⇤` | Next / previous field |
| | `↑` / `↓` | Move list or cycle format |
| | `↵` | Descend / pick / save / overwrite |
| | `Esc` | Cancel / close |
| **Query history** | `↑` / `↓` / `PgUp` / `PgDn` | Move selection |
| | `type` | Search filter |
| | `↵` | Paste into editor |
| | `d` | Delete entry |
| | `X` | Clear all (two-press) |
| | `⇥` | Toggle scope (this conn / all) |
| | `Esc` | Close |
| **Connection picker** | `↑` / `↓` | Move |
| | `↵` | Connect |
| | `a` / `e` / `x` | Add / edit / delete |
| | `K` | Unlink keyring entry |
| | `Esc` | Back |
| **Connection form** | `⇥` / `↓` | Next field |
| | `⇤` / `↑` | Previous field |
| | `←` / `→` | Cycle selected choice |
| | `↵` | Pick driver or submit |
| | `Ctrl+T` | Test network |
| | `Ctrl+L` | Test auth |
| | `Ctrl+S` | Save |
| | `Esc` | Cancel |
| **Driver / transport picker** | `type` | Filter |
| | `↑` / `↓` | Move |
| | `↵` | Select |
| | `Esc` | Cancel |
| **Safety prompts** | confirm run: `y` / `n` / `Esc` / `⇥` / `←` / `→` / `↵` | Run destructive DML/DDL guard |
| | SSH trust: `y` / `n` / `Esc` / `↵` | TOFU host-key accept (`↵` arms, then confirms) |
| **Command menu** (`Ctrl+K`) | `c` / `x` | Connect / Disconnect |
| | `h` | Query history |
| | `q` | Quit |
| | `Esc` | Cancel |


SQL files open in separate tabs. The editor normalizes line endings internally and
preserves UTF-8 (with or without BOM), BOM-marked UTF-16, and the file's predominant
LF/CRLF/CR line endings when saving. Invalid or unsupported text encodings produce
an error. Saves use a synced temporary file and reject changes made on disk since
the file was loaded or last saved; use Save As to choose a different destination.

Opening, saving, recovery writes, formatting, and large-document analysis run in
the background. Comment, indent, and selection-wrapping commands also run in the
background for large documents. Navigation and editing remain available; stale results cannot
replace newer edits. Above 1,000 lines or 128 KiB, syntax and search analysis run
asynchronously, so highlighting may briefly appear after the text. Completion
analysis also runs asynchronously above 128 KiB. Syntax analysis reuses unchanged
line states and cancels obsolete work. Dirty checks use saved history identities,
so undoing to a saved version clears the unsaved marker without comparing the
whole document. Undo stores changed ranges with
a 16 MiB budget and 256-command limit, retaining the newest command even if it
alone exceeds the budget.

To bound automatic analysis, syntax highlighting and completion stop above 8 MiB
or 100,000 lines. A line above 32,768 runes and its suffix display without syntax
highlighting, because its ending lexical state is unknown. Completion is also
limited on such a line. The editor reports these limits in text and preserves all
contents. Long Unicode lines build a sparse layout index as you navigate them.
Search stores at most 20,000 matches; narrow a capped search before using Replace
All. Cursor undo retains 50 navigation states and resets after a text edit.
Ctrl+D supports up to 256 selections of single-line text; edits across selections
share one undo step. Copy, cut, paste, and completion use the primary selection.

Dirty tabs are snapshotted every two seconds under sqlgo's data directory in
`recovery/`. Snapshots include SQL text and are private to your OS account on
systems that enforce Unix file permissions. Each snapshot is limited to 64 MiB;
an error appears in the status area if it exceeds this limit. After an interrupted
session, startup offers **R** to restore into new tabs, **D** to discard, or **Esc**
to decide later. Recovery covers the last successful snapshot; explicit discard
on quit removes that session's snapshot.

## CLI commands

sqlgo also provides verbs for scripting and direct startup actions. Most verbs run headlessly; `open` launches the TUI when no query source is supplied and runs headlessly with `-q`, `-f`, or piped SQL.

| Verb | Purpose |
|---|---|
| `exec` | run SQL and print results (default: table on a tty, TSV on a pipe) |
| `export` | run SQL and write results to a file or stdout (default: CSV) |
| `open` | open a SQLite database or query CSV/TSV/JSONL files without a saved connection |
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

### Open local data

Open one SQLite database directly in the TUI without creating a saved connection:

```sh
sqlgo open app.db
```

Common SQLite extensions (`.db`, `.db3`, `.s3db`, `.sqlite`, and `.sqlite3`) are recognized. Existing SQLite databases with other extensions are detected from their file header. The database is opened directly, so mutations are written to the original file.

CSV, TSV, and JSONL files are loaded into an ephemeral SQLite database. Each file becomes a table named after its filename, and multiple files can be opened together:

```sh
sqlgo open customers.csv orders.jsonl
```

Pass `-q`, `-f`, or piped SQL to query local data headlessly. Query and output flags must precede the file paths:

```sh
sqlgo open -q "select name from users order by name" app.db
sqlgo open --format jsonl -q "select * from customers" customers.csv
cat report.sql | sqlgo open customers.csv
```

SQLite databases must be opened one at a time. Opening flat files never modifies the source files.

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

# Open a local SQLite database directly in the TUI
sqlgo open app.db

# Save a new connection (password read from stdin, stored in OS keyring)
echo -n "$PGPASSWORD" | sqlgo conns add prod --driver postgres \
    --host db.local --port 5432 --user me --database app --password-stdin

# Verify the connection is reachable
sqlgo conns test prod

# Backup / restore connections
sqlgo conns export -o conns.json
sqlgo conns import -i conns.json
```

`conns export` omits keyring-backed passwords and replaces plaintext passwords, SSH passwords, tokens, passphrases, and inline credential options with redaction markers. Importing over an existing connection preserves its local secrets. A newly imported connection needs its credentials entered again.

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
