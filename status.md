Plan review — what's done, what's not
Grouped by the areas you organized the plan around. ✓ shipped, ◐ partial, ◻ not started.

Engine
✓ db.Capabilities + SchemaDepth / LimitSyntax enums; all driver-name string switches removed
✓ Streaming db.Rows interface with Next/Scan/Close, context cancellation, idempotent close
✓ MSSQL adapter (pre-existing, migrated to capabilities)
✓ Pure-Go SQLite adapter (internal/db/sqlite/sqlite.go) via modernc.org/sqlite, integration smoke test
✓ Pure-Go Postgres adapter (internal/db/postgres/postgres.go) via pgx/v5/stdlib
✓ Pure-Go MySQL adapter (internal/db/mysql/mysql.go) via go-sql-driver/mysql
◻ Integration tests that actually dial any of the four drivers — only DSN-level unit tests exist. A docker-backed smoke suite would be worth adding before cutting a release.
Execution / History
✓ SQLite-backed store.history table with ring-buffer trim (per-connection cap; configurable via SetHistoryRingMax)
✓ FTS5 index + prefix-expansion search (user matches users)
✓ Recording on every evtDone via app.recordHistory, degrades gracefully on store failure
✓ History browser overlay (history_layer.go) behind Space h — live FTS as you type, Enter pastes selected SQL into the editor
◐ Cross-connection browsing — data layer supports it (ListRecentHistory(ctx, "", N) / SearchHistory(ctx, "", ...)), but the overlay is per-connection only. Would be trivial to add a Tab binding to toggle scope.
◻ History delete / clear — no UI to forget a query. Ring-trim covers normal growth, but there's no "I typed my password into a query, wipe it" path.
Results
✓ Cell cursor (reverse-video highlight, Up/Dn/Lt/Rt/PgUp/PgDn/Home/End with auto-scroll)
✓ Clipboard copy: y cell, Y row (tab-separated)
✓ Filter — / overlay, live substring narrowing
✓ Sort — s cycles asc → desc → none on cursor column, header marker ^/v
✓ Escape chars rendered as dim \n \r \t inside cells (buffer preserves them, draw paints two-cell runs)
✓ Cell inspector popup — Enter on results, wraps multi-line values, y copies full value
✓ Export CSV / TSV / JSON / Markdown with format inferred from path extension
✓ Results-panel top-right border shows streaming N rows / N rows / 12ms / error
◐ Wrap mode escape-char styling — table.go:602 has a TODO; wrap mode currently paints escapes as plain text without the dim highlight. Non-wrap works correctly. Also the cell cursor highlight doesn't follow into wrap mode's per-line chunks.
◐ 100k-row buffer cap — hardcoded, no way to change from the UI. Probably fine for now.
◻ Copy all visible results — row/cell are wired, but there's no single-key "copy everything the filter is showing as TSV". Export covers it to a file; clipboard copy of the whole view doesn't exist.
◻ Search within column — filter is "any cell contains substring". No column-scoped or regex mode.
Editor
✓ Selection via shift-arrows (key decoder gained xterm modifier parsing — k.Shift)
✓ Ctrl+C copy, Ctrl+X cut, Ctrl+V paste (multi-line), Ctrl+A select-all
✓ Ctrl+Z undo, Ctrl+Y redo (snapshot stack, 256 deep, redo cleared on new edit)
✓ Selection highlight in the draw path, trailing-whitespace fill on multi-line selections
✓ Ctrl+C only steals from the editor when a query is running (fell through as copy otherwise)
◻ Syntax highlighting — never built. The Style + writeStyled + Theme plumbing is all in place from phase 1.9; what's missing is a SQL tokenizer and a pen-per-token loop in the editor draw.
◻ Token/context-aware autocomplete — never built. This is the single biggest gap from your original editor ask.
◻ Word-jump (Ctrl+Left/Ctrl+Right) and delete-word (Ctrl+Backspace/Ctrl+Delete). Small quality-of-life bindings that would land in ~30 lines.
◻ Find/replace inside the editor. Not asked for, but worth noting since everything else an IDE has is there now.
Security
✓ Keyring via zalando/go-keyring with ErrUnavailable sentinel and in-memory fallback
✓ Fallback-and-warn flow: every save reports saved (keyring) / saved (no keyring; plaintext) / saved (keyring write failed, stored plaintext)
✓ Placeholder sentinel on disk ({sqlgo:keyring}); edit form resolves it back to plaintext so the user sees the real password
✓ Per-engine options in the connection form via engine_spec.go — Driver field is a < Engine > cycler, per-engine option fields appear under the core block, dynamic rebuild preserves shared-key values
✓ SSH tunnel support — sshtunnel/tunnel.go opens a loopback listener that forwards through ssh.Client, works uniformly across every driver because it rewrites cfg.Host/Port
✓ SSH auth via password or private key (key wins)
✓ SSH password routes through the keyring under a name:ssh suffix
◐ tui.go:440 hardcodes encrypt=disable for every mssql connection that doesn't already have it set. This predates the engine_spec rewrite and is now redundant — the user gets an Encrypt field in the form, so the TUI shouldn't also force a default silently. Should be removed.
◐ SSH ssh.InsecureIgnoreHostKey() with a TODO at tunnel.go:82. Fine for "same as sqlit" baseline, but not acceptable for anything on the public internet.
◻ Known-hosts handling — see above.
◻ Connection options validation — sslmode, encrypt, tls all take free-form strings. A picker for the known valid values would be a nice follow-up.
◻ Keyring unlink — there's no UI to wipe a stored secret without deleting the whole connection. If the user switches back to plaintext, the old keyring entry stays orphaned until delete.
Explorer
◐ Engine-aware schema depth is still not implemented. The explorer renders every engine as schema → Tables/Views → leaf, even SQLite which reports SchemaDepthFlat. Today you'll see a main parent node under SQLite. The capability is read and the enum exists; explorer.go:202 rebuild() just doesn't branch on it. This should be a small change — read caps.SchemaDepth (pass via SetSchema or look it up on the connection) and skip the schema group when flat.
Theming
✓ Style{FG,BG,Attrs} type, Theme struct in theme.go, currentTheme indirection, background + attrs in the flush diff loop (writeStyleTransition)
◻ User theme file loader. All the seams exist; nothing reads a file yet. Per your instructions this was a "wait on" item, so this is expected.
Other / infrastructure
✓ Streaming query runner with Ctrl+C cancel (throws away buffered rows on cursor close)
✓ Connection store + JSON export/import for hand-editing
✓ Legacy connections.json one-shot import on first boot
✓ Clipboard wrapper with headless fallback
✓ F8 hidden debug overlay (key inspector)
✓ Results panel reset on connect (editor cleared, table cleared, focus lands on Explorer)
✓ ASCII-only hint strings (no decorative Unicode)
◐ Multi-query tabs — deferred by you, still deferred.
◻ SSH tunnel tests. internal/sshtunnel has no test file. Would need a real sshd or a mock — not easy, but the accept loop is non-trivial enough to be worth exercising.
◻ Postgres/MySQL/MSSQL integration tests. Only DSN unit tests exist. A docker-compose spun at test time would give real coverage.
Bugs / rough edges I'd fix first
Explorer flat-schema rendering — small, user-visible, obviously wrong for SQLite.
mssql encrypt=disable default in tui.go:440 — now redundant with the engine spec and arguably unsafe. Either delete or move to the engine spec's default value.
Wrap-mode cell highlight + escape styling — the TODO in the wrap branch. Minor visual inconsistency between wrap and non-wrap modes.
Nothing stops a "connect failed" error message from running off the right edge of the picker status line — cosmetic, but long DSN errors get truncated mid-word. Not a regression but worth noting.
Stuff sqlit probably has that we never discussed
Things I see in similar TUIs that aren't in the plan at all. Flagging in case you want any of them on the roadmap later:

Explain plan visualization (pre-parse the query, run EXPLAIN, show tree)
Schema diff between two connections
Saved queries / bookmarks (distinct from history — curated)
Backup/restore (engine-specific dump)
Multi-cursor editing — very IDE-ish, probably overkill
Query templates / snippets
Bottom line: everything in the four areas you scoped for this session (engine, execution, results, security) and the Editor group is done, with three caveats: wrap-mode escape styling, editor syntax highlight, and editor autocomplete. The Explorer gap (flat-schema rendering for SQLite) was never touched and is the single clearest "shipped in plan 1, slipped through" item. Theming is intentionally deferred.