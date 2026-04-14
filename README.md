<div align="center">

# sqlgo

**A fast, keyboard-driven TUI SQL client for PostgreSQL, MySQL, SQL Server, and SQLite.**

</div>

---

## Why sqlgo?

Heavy GUI clients are slow to launch, hard to script around, and hostile over SSH. `psql` and `mysql` are great until you want a result pane you can actually scroll. sqlgo sits in the middle: a terminal-native workbench that starts in milliseconds, runs everywhere, and works the same against every engine it supports.

No mouse required. No Electron. No cgo. One static binary.

## Features

### Databases
- **PostgreSQL** via `pgx/v5`
- **MySQL / MariaDB** via `go-sql-driver/mysql`
- **SQL Server** via `go-mssqldb`
- **SQLite** via `modernc.org/sqlite` (pure Go, no cgo)

### Workbench
- **Three-panel layout** -- Explorer (schema tree), Query (editor), Results (table). Toggle focus with `Alt+1/2/3`.
- **Fullscreen editor** -- `F11` maximizes the query pane.
- **Streaming results** -- rows land live with a running count; cancel mid-query with `Ctrl+C`.
- **Multi-connection** -- saved connections switch at runtime from the command menu.

### Editor
- Multi-line with **undo/redo** (`Ctrl+Z` / `Ctrl+Y`)
- **Find / replace** (`Ctrl+F`)
- **Autocomplete** on column names cached per-connection (`Ctrl+Space`)
- **Multi-cursor** (`Ctrl+Alt+Up/Down`)
- **SQL formatter** (`Alt+F`)
- **Bracketed paste** and standard cut/copy/paste

### Schema Explorer
- Browse schemas, tables, and views
- `Enter` or `s` on a table drops a driver-aware `SELECT ... LIMIT 100` into the editor
- `R` refreshes the schema

### Results
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

### Power Features
- **SSH tunneling** -- optional jump host per connection with password or key-file auth and TOFU host-key prompts. `ssh-agent` is not supported; supply a key file or password.
- **OS keyring** -- passwords are stored in the system keychain when available (falls back to plain store with a warning)
- **Query history** -- last 1000 queries per connection, FTS5-indexed, retrievable from the history overlay
- **EXPLAIN overlay** -- run the current query through its engine's explain path (Postgres, MySQL, SQLite; MSSQL is not supported)
- **Open file** -- `o` opens a workspace file picker rooted at the current directory; honors a `.sqlgoignore` file for directory skips

### Companion Tools
- **`sqlgoseed`** -- populates any supported engine with a fictional-company dataset. `-scale` multiplies row counts (scale=1 gives ~3-5k rows; scale=100 gives hundreds of thousands).
- **`sqlgocheck`** -- smoke-test connection and query utility, useful for scripting health checks.

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

Installs to `%LOCALAPPDATA%\sqlgo\sqlgo.exe` and adds that directory to your user `PATH`.

### From source

```sh
go install github.com/Nulifyer/sqlgo/cmd/sqlgo@latest
```

### Uninstall

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.sh | bash

# Windows
irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.ps1 | iex
```

---

## Usage

Launch the TUI:

```sh
sqlgo
```

From the command menu (`Space`) you can connect, disconnect, export, view history, and run `EXPLAIN`. Everything else is keyboard-driven.

### Keybindings

| Context | Key | Action |
|---|---|---|
| **Global** | `Ctrl+Q` | Quit |
| | `Alt+1` / `Alt+2` / `Alt+3` | Focus Explorer / Query / Results |
| | `F8` | Key-debug overlay |
| **Query editor** | `F5` / `Ctrl+Enter` | Run query |
| | `Ctrl+C` | Cancel running query |
| | `F11` | Toggle fullscreen editor |
| | `Alt+F` | Format SQL |
| | `Ctrl+Z` / `Ctrl+Y` | Undo / Redo |
| | `Ctrl+Space` | Autocomplete |
| | `Ctrl+F` | Find / replace |
| | `Ctrl+Alt+Up/Dn` | Add multi-cursor line |
| | `Ctrl+L` | Clear editor |
| **Explorer** | `Enter` / `s` | Generate `SELECT` for table |
| | `R` | Refresh schema |
| **Results** | `y` / `Y` / `Alt+A` | Copy cell / row / all |
| | `s` | Cycle sort on column |
| | `/` | Filter rows |
| | `w` | Toggle word-wrap |
| | `Enter` | Inspect cell |
| **Command menu** (`Space`) | `c` / `x` | Connect / Disconnect |
| | `o` | Open SQL file |
| | `e` | Export results |
| | `h` | Query history |
| | `p` | EXPLAIN current query |
| | `q` | Quit |

---

## Storage

sqlgo keeps per-user state under `~/.sqlgo/`:

- `sqlgo.db` -- SQLite store holding saved connections and query history (WAL mode)
- `connections.json` -- legacy JSON file, auto-imported once on first run

Passwords go to the OS keyring when one is available (macOS Keychain, Windows Credential Manager, libsecret on Linux). SSH passwords are stored under their own keyring entry.

### Environment Variables

| Variable | Default | Effect |
|---|---|---|
| `SQLGO_ROW_CAP` | `100000` | Max rows buffered per result set |
| `SQLGO_BYTE_CAP` | `268435456` (256 MiB) | Max bytes buffered per result set |
| `SQLGO_DEBUG` | unset | When set to `1`, panics dump a stack trace to `sqlgo-panic-<unix>.log` in the working directory |

---

## Development

### Prereqs
- Go (version tracked in `go.mod`)
- Podman or Docker for the dev databases (compose stack in [compose.yaml](compose.yaml))

### Run the dev databases

```sh
podman compose up -d
```

Brings up MSSQL (port `11433`), Postgres (`15432`), MySQL (`13306`), and an SSH bastion (`12222`). Credentials are in [compose.yaml](compose.yaml) -- **dev only**.

### Build and run

```sh
go run ./cmd/sqlgo
```

### Dev deploy

Builds all three binaries (`sqlgo`, `sqlgocheck`, `sqlgoseed`) with a `dev-<shortsha>` version tag and installs them to your local `bin` directory:

```sh
# Linux / macOS
./.scripts/dev-deploy.sh

# Windows
.\.scripts\dev-deploy.ps1
```

### Release

Tag-triggered via GoReleaser. From the repo root:

```sh
# Linux / macOS
./.scripts/release.sh

# Windows
.\.scripts\release.ps1
```

The script shows recent tags and commits since the last tag, prompts for the next `vX.Y.Z`, then tags and pushes. The [release workflow](.github/workflows/release.yml) builds archives for linux/macOS/windows on amd64 and arm64, generates `checksums.txt`, and publishes a GitHub Release.

### Seed a database

```sh
go run ./cmd/sqlgoseed \
    -driver postgres \
    -host localhost -port 15432 \
    -user sqlgo -pass sqlgo_dev \
    -db sqlgo_test \
    -scale 5
```

---

## License

See [LICENSE](LICENSE).
