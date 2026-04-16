# Architecture

High-level map of the sqlgo process. Aimed at contributors reading the
code for the first time. File references are the authoritative
source -- this doc points at them, it does not replace them.

## Process shape

One goroutine owns the UI. Everything else (key reading, query
execution, schema fetch, history writes, SSH I/O) runs off the main
goroutine and communicates back through channels. The main goroutine
never blocks on a network or disk call; it blocks only on `select`
over event channels.

```
+-------------------+       +--------------------+
| stdin reader      | ----> | msgCh (InputMsg)   | --+
+-------------------+       +--------------------+   |
                                                     v
+-------------------+       +--------------------+  +------------------+
| query goroutine   | ----> | resultCh           |->| main event loop  |
+-------------------+       +--------------------+  | (tui.go:Run)     |
                                                    +------------------+
+-------------------+       +--------------------+   ^
| async workers     | ----> | asyncCh func(*app) | --+
+-------------------+       +--------------------+
```

The loop lives in [tui.go](../internal/tui/tui.go) (`for !a.quit { ... }`
around line 395). Each tick it: applies declarative terminal modes,
draws all layers, diff-flushes the screen, then `select`s on one of
the input/result/async/resize/signal channels.

## Layer stack

The UI is a stack of [Layer](../internal/tui/layer.go) values. The
bottom layer is always `*mainLayer` (explorer + editor + results);
overlays (picker, form, history, find, confirm, etc.) push on top.

- Only the topmost layer receives input.
- Every layer draws into its own transparent `cellbuf`; the screen
  composites bottom-to-top.
- Layers mutate `*app` directly -- there is no message bus. "Pick a
  connection, then dismiss the picker" is plain imperative code.
- A layer that implements `ViewProvider` declares the terminal modes
  (alt-screen, mouse, paste, window title) it wants while on top. The
  topmost provider wins.

Extension hooks:

- `Layer.HandleKey` -- always called on the topmost layer.
- `InputHandler.HandleInput` -- optional, for mouse / paste / focus /
  resize. See [events.go](../internal/tui/events.go).

## Screen and cell diff

[screen.go](../internal/tui/screen.go) keeps two cell buffers (current
and previous) and emits ANSI only for cells that changed. That is why
the redraw cost is flat -- a thousand unchanged rows cost nothing.

`applyView` tracks the last applied `View` and only emits
alt-screen / mouse / paste / title sequences for flags that flipped.
Per-frame `View()` calls are cheap when nothing changes.

The window title goes through `sanitizeWindowTitle` before it lands in
the OSC 2 sequence so a connection name with ESC/BEL/ST cannot break
out of the escape.

## Async boundary

Two channels carry work back to the main loop:

- **`resultCh chan queryEvent`** -- query lifecycle. A running query's
  goroutine emits `evtResultSetStart` / `evtProgress` /
  `evtResultSetDone` / `evtDone` in order. The main loop's
  `handleQueryEvent` routes each event to the right `*session`/tab --
  the goroutine does not touch app state. See
  [tui.go](../internal/tui/tui.go) and
  [session.go](../internal/tui/session.go).
- **`asyncCh chan func(*app)`** -- generic callbacks. Background work
  (schema fetch, history write, capability probes) runs in a
  goroutine; when it finishes it posts a closure that the main loop
  runs with `*app` in scope. The closure is the only place that
  touches UI state.

The callback pattern looks like:

```go
conn := a.conn           // snapshot: conn may be replaced before we return
go func() {
    info, err := conn.Schema(ctx)
    a.asyncCh <- func(a *app) {
        if a.conn != conn { // user disconnected / switched; drop it
            return
        }
        // update UI state here
    }
}()
```

Always snapshot the `*app` fields the goroutine reads, and re-check
them inside the callback -- the user can disconnect, switch tabs, or
quit between the two.

## Sessions and result tabs

A `*session` ([session.go](../internal/tui/session.go)) owns one query
tab: editor buffer, result-tab list, per-tab cancel handle, dirty
tracking, source-path bookkeeping.

Per-session state (`running`, `cancel`, `lastQuerySQL`) lives on the
session rather than the app so a long-running query on one tab does
not block `Run` on another. The underlying `*sql.DB` pool is already
goroutine-safe; parallel sessions just need independent cancel
handles.

Result tabs (`*resultTab`) are produced one per result set -- a
multi-statement batch whose driver supports `NextResultSet` fans out
into one tab per set.

## Driver plugin

Engines live in [internal/db/](../internal/db/). Each package:

1. Imports the engine's `database/sql` driver for side effects.
2. Implements `db.Driver` and registers via `db.Register` in `init()`.
3. Exports a `Profile` (dialect: schema queries, capabilities, explain)
   and a `Transport` (wire driver: DSN builder or custom opener).
4. Registers both via `db.RegisterProfile` / `db.RegisterTransport`.
5. The preset `Driver.Open` delegates to `db.OpenWith(ctx, Profile,
   Transport, cfg)` which composes them into a live `db.Conn` through
   the package-internal `openSQL` wrapper.

**Profile** is the dialect brain: schema/columns queries, capabilities,
definition fetcher, explain runner. Portable across wire transports --
the same Sybase ASE profile can be driven by a native TDS transport
today or an ODBC bridge tomorrow.

**Transport** is the wire half: a `database/sql` driver name plus
either a `BuildDSN` function or a custom `Open` (for drivers needing
pre-open work like file ingestion). One transport can back many
profiles (TDS -> mssql + sybase).

The TUI's "Other..." connection flow lets users pick profile and
transport independently, stored as `config.Connection.Profile` and
`.Transport`. When both are set, `connectTo` calls `db.OpenWith`
directly instead of going through the preset `Driver.Open`.

The adapter author almost never writes a `db.Conn`. The shared wrapper
handles streaming `Rows`, `Schema`, `Columns`, `Ping`, `Exec`,
`Close`, `NextResultSet`, and the `[]byte -> string` coercion.

See [adding-a-db-adapter.md](adding-a-db-adapter.md) for the full
contract (SchemaQuery columns, ColumnsQuery vs ColumnsBuilder,
Capabilities flags, system-object detection).

## Persistent storage

[internal/store/](../internal/store/) is a SQLite-backed store with
migrations and FTS5-indexed history. The process talks to it through
short-lived contexts so a slow disk never hangs the UI.

- Connections are stored as rows; passwords are either literal or the
  sqlgo keyring placeholder.
- Query history is kept per connection, capped at 1000 entries,
  retrievable via an FTS5 match.
- The store file lives at `<data dir>/sqlgo.db` (WAL mode), where
  `<data dir>` is `$XDG_DATA_HOME/sqlgo` on Linux,
  `~/Library/Application Support/sqlgo` on macOS, or
  `%LocalAppData%\sqlgo` on Windows. Legacy `~/.sqlgo/sqlgo.db` is
  migrated on first run.

The OS keyring ([internal/secret/](../internal/secret/)) is best-effort:
if the backend is available, new passwords go there and the store row
holds only the placeholder; if not, plaintext-on-disk with a one-time
warning.

## SSH tunneling

See [ssh-tunneling.md](ssh-tunneling.md). The tunnel appears to the
driver as a plain `127.0.0.1:PORT` socket, so every adapter gets SSH
support with no engine-specific code.

## SQL safety classifier

[internal/sqltok/](../internal/sqltok/) tokenizes SQL and classifies
the leading statement. The TUI uses the classifier to gate destructive
statements (DELETE / UPDATE / DROP / TRUNCATE / MERGE without a WHERE
that references something) behind a confirm overlay. CTEs
(`WITH cte AS (...) DELETE FROM t`) are unwrapped so the effective
keyword is what gets classified, not `WITH`.

## Testing layout

- Unit tests sit next to their package and run under
  `go test ./...` on a clean checkout.
- Driver integration tests that need a live server sit behind a
  `//go:build integration` tag (see
  [postgres_integration_test.go](../internal/db/postgres/postgres_integration_test.go)).
- The dev compose stack in [compose.yaml](../compose.yaml) is what
  those tests target; the seed scripts in `.scripts/` populate it.
