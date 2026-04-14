package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Nulifyer/sqlgo/internal/clipboard"
	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// queryEventKind tags the lifecycle phase of a running query. A single
// query can emit any number of evtProgress updates between an evtStarted
// (or an immediate-failure evtDone) and its final evtDone.
type queryEventKind int

const (
	// evtResultSetStart: a new result set is about to stream into the
	// attached tab. The goroutine has already called Init() on the tab's
	// table; the main loop needs to register the tab on the session so
	// the user sees it in the tab bar.
	evtResultSetStart queryEventKind = iota
	// evtResultSetDone: the attached tab's result set finished. The main
	// loop writes the per-tab summary (row count, elapsed, cap flags)
	// from this event.
	evtResultSetDone
	// evtProgress: periodic row-count update. Non-authoritative and
	// dropped if the main loop is busy -- the final count arrives in
	// evtResultSetDone.
	evtProgress
	// evtDone: the query has finished, either cleanly (err==nil) or with
	// an error (including context.Canceled on user cancel).
	evtDone
)

// queryEvent is posted on the result channel during a query's lifetime.
// Moving status updates through the same channel as the final result
// gives the main loop a single select-case for everything query-related.
type queryEvent struct {
	kind      queryEventKind
	loaded    int
	capped    bool
	capReason string
	err       error
	elapsed   time.Duration
	// tab is the *resultTab this event applies to. Set on
	// evtResultSetStart / evtResultSetDone; nil otherwise.
	tab *resultTab
	// sess is the session (query tab) that produced this event.
	// Required so the main loop can route the update to the right
	// tab even when the user has switched away from it mid-query.
	sess *session
}

// app is the top-level TUI state. The UI is composed of a stack of Layers;
// draw loops run bottom-to-top and input goes to the top-most layer only.
// Connection and async query state live on the app because multiple layers
// (main view, picker, future popups) need to touch them.
type app struct {
	term *terminal
	scr  *screen

	layers []Layer

	// Persistent state. Connections (and later history) live in a sqlite
	// file the TUI manages via internal/store. connCache is refreshed from
	// the store on every mutation so the picker's Draw can stay free of
	// IO without going stale.
	store     *store.Store
	connCache []config.Connection

	// clipboard is the system clipboard abstraction. Shared across the
	// app so copy/paste code paths in the results panel, editor, and
	// future widgets all go through the same mapErr sentinel handling.
	clipboard clipboard.Clipboard

	// secrets is the OS keyring abstraction. When the backend is
	// available we move new passwords off disk into it on save; when
	// it's not, we fall back to plaintext in the store and surface a
	// warning once. secretsAvailable is cached at boot.
	secrets          secret.Store
	secretsAvailable bool

	// Active connection.
	conn       db.Conn
	connErr    error
	activeConn *config.Connection
	// tunnel is the SSH jump connection, if the active connection
	// routes through one. Closed in disconnect() after the db.Conn,
	// so any lingering reads on the forwarded socket get the right
	// "driver closed" error first.
	tunnel *sshtunnel.Tunnel

	// Async query. The resultCh is a single pump shared across sessions;
	// per-session runner state (running/cancel/lastQuerySQL/lastQueryStart)
	// lives on *session so parallel tabs don't fight over one cancel handle.
	resultCh chan queryEvent

	// columnCache memoizes editor autocomplete lookups.
	// Cleared on disconnect so fresh schema wins.
	columnCache *columnCache

	quit bool
}

// refreshConnections re-reads the saved-connections list from the store
// into connCache. Called after every mutation (save, delete, rename) so
// the picker's next Draw sees the change.
func (a *app) refreshConnections() error {
	ctx, cancel := context.WithTimeout(context.Background(), storeReadTimeout)
	defer cancel()
	list, err := a.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	a.connCache = list
	return nil
}

// resolvePassword returns the real password for a saved connection. When
// the stored password is the sqlgo keyring placeholder, we fetch the
// value from the OS secret store; otherwise the stored string is
// already the plaintext. Errors bubble up so the caller can show the
// user exactly why a connect failed.
func (a *app) resolvePassword(c config.Connection) (string, error) {
	if c.Password != secret.Placeholder {
		return c.Password, nil
	}
	if a.secrets == nil {
		return "", fmt.Errorf("password in keyring but no secret store available")
	}
	pass, err := a.secrets.Get(c.Name)
	if err != nil {
		return "", fmt.Errorf("keyring get %q: %w", c.Name, err)
	}
	return pass, nil
}

// sshKeyringAccount is the account name used when storing the SSH
// tunnel password for a connection in the keyring. We suffix the
// connection name so the db password and the ssh password can live
// as two separate entries under the same sqlgo service.
func sshKeyringAccount(connName string) string {
	return connName + ":ssh"
}

// persistConnection upserts a connection via the store and, when the
// OS keyring is available, also rewrites the row to use the sqlgo
// keyring placeholder so the plaintext password never lands on disk.
// oldName carries any pre-rename identifier so the store's atomic
// rename path is used.
//
// On keyring failure we fall through to plaintext-on-disk: this is a
// deliberate fallback-and-warn choice so the app stays usable on hosts
// without a secret backend. The ok return lets callers surface the
// warning exactly once per save.
func (a *app) persistConnection(ctx context.Context, oldName string, c config.Connection) (usedKeyring bool, err error) {
	// If the caller handed us the placeholder, the secret is already
	// in the keyring (edit path with no password change). Save the
	// row as-is.
	if c.Password == secret.Placeholder {
		return true, a.store.SaveConnection(ctx, oldName, c)
	}

	if a.secretsAvailable && a.secrets != nil {
		// Best-effort: write the secret first, then the row with the
		// placeholder. If the secret write fails, fall back to
		// plaintext rather than losing the password entirely.
		if werr := a.secrets.Set(c.Name, c.Password); werr == nil {
			rowCopy := c
			rowCopy.Password = secret.Placeholder

			// SSH password: mirror the same fallback-and-warn flow so
			// the jump-host secret never lands on disk when we have
			// a keyring. Empty SSH passwords and placeholder values
			// (edit path with no change) are left alone.
			if rowCopy.SSH.Password != "" && rowCopy.SSH.Password != secret.Placeholder {
				if err := a.secrets.Set(sshKeyringAccount(c.Name), rowCopy.SSH.Password); err == nil {
					rowCopy.SSH.Password = secret.Placeholder
				}
			}

			if sErr := a.store.SaveConnection(ctx, oldName, rowCopy); sErr != nil {
				// Store write failed after secret write: try to undo
				// both secret writes so we don't leak orphan entries.
				_ = a.secrets.Delete(c.Name)
				_ = a.secrets.Delete(sshKeyringAccount(c.Name))
				return false, sErr
			}
			// If this was a rename, delete the old keyring entries.
			if oldName != "" && oldName != c.Name {
				_ = a.secrets.Delete(oldName)
				_ = a.secrets.Delete(sshKeyringAccount(oldName))
			}
			return true, nil
		}
		// Fall through to plaintext on secret-write failure.
	}

	if err := a.store.SaveConnection(ctx, oldName, c); err != nil {
		return false, err
	}
	return false, nil
}

// deleteConnection removes a connection from the store and, if the
// keyring is available, best-effort-removes any matching secret entry
// (including the SSH-tunnel suffixed entry) so uninstalling a
// connection doesn't leak its password.
func (a *app) deleteConnection(ctx context.Context, name string) error {
	if err := a.store.DeleteConnection(ctx, name); err != nil {
		return err
	}
	if a.secrets != nil {
		_ = a.secrets.Delete(name)
		_ = a.secrets.Delete(sshKeyringAccount(name))
	}
	return nil
}

// unlinkSecret removes the keyring entries for a connection without
// deleting the store row. Callers that don't want to lose the rest
// of the connection config (host/port/options/etc) but do want the
// password gone -- e.g. because the keyring is going stale and they
// plan to re-enter the password on next connect -- use this path.
// Store rows whose password was the placeholder get their password
// field cleared so the next connectTo doesn't try to resolve a
// secret that no longer exists.
func (a *app) unlinkSecret(ctx context.Context, name string) error {
	if a.secrets == nil {
		return fmt.Errorf("no secret store available")
	}
	// Best-effort delete -- neither entry necessarily exists.
	_ = a.secrets.Delete(name)
	_ = a.secrets.Delete(sshKeyringAccount(name))

	// Clear any placeholder in the store row so the connection
	// doesn't end up pointing at a secret that was just deleted.
	c, err := a.store.GetConnection(ctx, name)
	if err != nil {
		return err
	}
	changed := false
	if c.Password == secret.Placeholder {
		c.Password = ""
		changed = true
	}
	if c.SSH.Password == secret.Placeholder {
		c.SSH.Password = ""
		changed = true
	}
	if changed {
		if err := a.store.SaveConnection(ctx, "", c); err != nil {
			return err
		}
	}
	return nil
}

// Options configures a Run invocation. The zero value is valid and
// matches the pre-Options behavior.
type Options struct {
	// InitialQuery, if non-empty, seeds the query editor with this text
	// on startup. Used by the CLI `sqlgo file.sql` entry point.
	InitialQuery string
}

// Run takes over the terminal and runs until the user quits (Ctrl+Q) or an
// error occurs. The terminal is always restored before return.
func Run(opts Options) error {
	t, err := openTerminal()
	if err != nil {
		return err
	}
	// Panic handler runs before t.Restore so it can emit the screen-
	// unsetup sequences while stdout is still ours, then hand back to
	// cooked mode. It re-panics, so t.Restore below is a no-op on the
	// panic path but still covers the clean-exit path.
	defer restoreTerminalOnPanic(t)
	defer t.Restore()

	// Alt-screen and cursor-hide are handled declaratively per-frame
	// via screen.applyView; the first flush emits both because the
	// view baseline is the zero value. The defer restores the main
	// screen on clean exit -- panic path goes through
	// restoreTerminalOnPanic.
	fmt.Fprint(os.Stdout, cursorHide)
	defer fmt.Fprint(os.Stdout, cursorShow)

	sec := secret.System()
	secAvail := sec.Available()

	a := &app{
		term: t,
		scr:  newScreen(os.Stdout, t.width, t.height),
		// Buffer a handful of events so a fast-streaming query doesn't
		// stall the drain goroutine on a non-blocking progress send.
		resultCh:         make(chan queryEvent, resultChanBuf),
		clipboard:        clipboard.System(),
		secrets:          sec,
		secretsAvailable: secAvail,
	}
	ml := newMainLayer()
	if opts.InitialQuery != "" {
		ml.editor.buf.SetText(opts.InitialQuery)
	}
	a.layers = []Layer{ml}

	// Open the persistent store (connections, history) and migrate it.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), storeBootTimeout)
	st, err := store.Open(bootCtx)
	cancelBoot()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	a.store = st

	if err := a.refreshConnections(); err != nil {
		return fmt.Errorf("load connections: %w", err)
	}

	// Start on the picker so the user picks or creates a connection first.
	a.pushLayer(newPickerLayer(a.connCache))

	defer func() {
		if a.conn != nil {
			_ = a.conn.Close()
		}
		if a.tunnel != nil {
			_ = a.tunnel.Close()
		}
		_ = a.store.Close()
		// Emit the off-sequences for whatever terminal modes the last
		// applied View had on. Panic path skips this (handled inline by
		// restoreTerminalOnPanic).
		a.scr.teardownView()
	}()

	return a.loop()
}

func (a *app) loop() error {
	keys := newKeyReader(stdinReader())
	msgCh := make(chan InputMsg, inputChanBuf)
	keyErrCh := make(chan error, 1)
	go func() {
		for {
			m, err := keys.Read()
			if err != nil {
				keyErrCh <- err
				close(msgCh)
				return
			}
			msgCh <- m
		}
	}()

	// SIGINT/SIGTERM from outside the terminal (e.g. `kill`) should
	// exit cleanly through the same path as Ctrl+Q. In raw mode
	// Ctrl+C is delivered as a 0x03 keystroke, not as SIGINT, so
	// this channel only fires for external signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Resize source: SIGWINCH on Unix, polling ticker on Windows.
	// Either way a message on resizeCh means "recheck terminal size";
	// refreshSize() below decides whether the screen actually changed.
	resizeCh, stopResize := watchResize()
	defer stopResize()

	for !a.quit {
		if a.term.refreshSize() {
			a.scr.resize(a.term.width, a.term.height)
		}
		// Apply declarative terminal modes (alt-screen, mouse, paste,
		// title) before the cell-diff flush so the next diff lands in
		// the right buffer.
		if err := a.scr.applyView(a.effectiveView()); err != nil {
			return fmt.Errorf("apply view: %w", err)
		}
		a.draw()
		if err := a.scr.flush(); err != nil {
			return fmt.Errorf("flush: %w", err)
		}

		select {
		case m, ok := <-msgCh:
			if !ok {
				if err := <-keyErrCh; err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("read key: %w", err)
				}
				return nil
			}
			a.handleInput(m)
		case e := <-a.resultCh:
			a.handleQueryEvent(e)
		case <-resizeCh:
			// Wake: loop top calls refreshSize() and redraws.
		case <-sigCh:
			a.quit = true
		}
	}
	return nil
}

// draw renders the current frame. Each layer draws into its own cell
// buffer (transparent cells pass through to the layer below); the screen
// then composites them bottom-to-top and diffs against the prior frame
// on flush, so only changed cells get emitted as ANSI.
func (a *app) draw() {
	bufs := make([]*cellbuf, len(a.layers))
	for i, l := range a.layers {
		b := a.scr.layerBuf(i)
		l.Draw(a, b)
		bufs[i] = b
	}
	a.scr.composite(bufs)
}

// handleInput routes any InputMsg to the right handler. Key messages
// go through the long-standing handleKey path so every existing layer
// keeps working unchanged. Non-Key messages (Mouse, Paste, Focus, Blur)
// are delivered to the top layer only if it implements InputHandler;
// otherwise they're dropped silently, which matches the pre-v2 behavior
// of the terminal ignoring these escape sequences entirely.
func (a *app) handleInput(m InputMsg) {
	switch v := m.(type) {
	case Key:
		a.handleKey(v)
	default:
		if h, ok := a.topLayer().(InputHandler); ok {
			h.HandleInput(a, m)
		}
	}
}

// handleKey sends the key to the topmost layer, with two global escape
// hatches: Ctrl+Q quits, and F8 opens the hidden key-debug overlay. F8
// is handled here (not in a layer) so it's reachable from any modal.
func (a *app) handleKey(k Key) {
	if k.Ctrl && k.Rune == 'q' {
		a.quit = true
		return
	}
	if k.Kind == KeyF1 {
		if _, ok := a.topLayer().(*helpLayer); ok {
			a.popLayer()
			return
		}
		a.pushLayer(newHelpLayer())
		return
	}
	if k.Kind == KeyF8 {
		// Toggle: if the debug layer is already on top, closing it is
		// the expected outcome of pressing the same key again. Otherwise
		// push a fresh one.
		if _, ok := a.topLayer().(*debugLayer); ok {
			a.popLayer()
			return
		}
		a.pushLayer(newDebugLayer())
		return
	}
	a.topLayer().HandleKey(a, k)
}

// --- connection lifecycle --------------------------------------------------

// connectTo dials the given connection and, on success, replaces the active
// connection and drops back to the main view. Any previous connection is
// closed only after the new one is established, so a failed switch doesn't
// leave us disconnected.
func (a *app) connectTo(c config.Connection) {
	pl, _ := a.topLayer().(*pickerLayer)

	d, err := db.Get(c.Driver)
	if err != nil {
		if pl != nil {
			pl.setStatus(err.Error())
		}
		return
	}
	pass, err := a.resolvePassword(c)
	if err != nil {
		if pl != nil {
			pl.setStatus(err.Error())
		}
		return
	}
	cfg := db.Config{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: pass,
		Database: c.Database,
		Options:  c.Options,
	}

	// Optional SSH jump. Open the tunnel first, then rewrite the dial
	// target to the loopback address it exposes. On any error the
	// tunnel is torn down before we return so partially-constructed
	// state never escapes this function.
	var tunnel *sshtunnel.Tunnel
	if c.SSH.Host != "" {
		if pl != nil {
			pl.setStatus("ssh tunnel: dialing…")
			a.draw()
			_ = a.scr.flush()
		}
		sshPass := c.SSH.Password
		if sshPass == secret.Placeholder && a.secrets != nil {
			if resolved, err := a.secrets.Get(sshKeyringAccount(c.Name)); err == nil {
				sshPass = resolved
			} else {
				if pl != nil {
					pl.setStatus("ssh keyring get: " + err.Error())
				}
				return
			}
		}
		tcfg := sshtunnel.Config{
			SSHHost:     c.SSH.Host,
			SSHPort:     c.SSH.Port,
			SSHUser:     c.SSH.User,
			SSHPassword: sshPass,
			SSHKeyPath:  c.SSH.KeyPath,
			TargetHost:  c.Host,
			TargetPort:  c.Port,
		}
		t, err := sshtunnel.Open(tcfg)
		if err != nil {
			// TOFU: unknown host → push trust overlay; accept
			// retries connectTo with the same target.
			var unknown *sshtunnel.UnknownHostError
			if errors.As(err, &unknown) {
				a.pushLayer(newTrustLayer(c, unknown))
				return
			}
			// Key mismatch is fatal -- no override path.
			var mismatch *sshtunnel.HostKeyMismatchError
			if errors.As(err, &mismatch) {
				if pl != nil {
					pl.setStatus(mismatch.Error())
				}
				return
			}
			if pl != nil {
				pl.setStatus("ssh tunnel: " + err.Error())
			}
			return
		}
		tunnel = t
		cfg.Host = t.LocalHost
		cfg.Port = t.LocalPort
	}

	if pl != nil {
		pl.setStatus("connecting…")
		// Flush the status update before we block on Open so the user
		// sees feedback.
		a.draw()
		_ = a.scr.flush()
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		// Tear down the just-opened tunnel so a dial failure doesn't
		// leak the listener + SSH client into the background.
		if tunnel != nil {
			_ = tunnel.Close()
		}
		if pl != nil {
			pl.setStatus("connect failed: " + err.Error())
		}
		return
	}

	// Commit the new connection. Close the previous db conn and its
	// tunnel (if any) first so the old listener goes away before the
	// new one's state becomes visible.
	if a.conn != nil {
		_ = a.conn.Close()
	}
	if a.tunnel != nil {
		_ = a.tunnel.Close()
	}
	if a.columnCache != nil {
		a.columnCache.clear()
	}
	a.conn = conn
	a.tunnel = tunnel
	cc := c
	a.activeConn = &cc
	a.connErr = nil

	// Dismiss the picker and reset the main-view state. Focus lands on
	// the explorer (not the query editor) because the most common first
	// action after connecting is "browse the schema", and any prior
	// query text / results from the previous connection are wiped so
	// the user doesn't accidentally run them against the new one.
	a.popLayer()
	m := a.mainLayerPtr()
	m.status = "connected"
	m.editor.buf.Clear()
	m.resetResults()
	m.focus = FocusExplorer
	a.loadSchema()
}

func (a *app) disconnect() {
	if a.conn == nil {
		return
	}
	_ = a.conn.Close()
	a.conn = nil
	// Close the tunnel after the db conn so any lingering reads on
	// the forwarded socket get the driver's "closed" error first.
	if a.tunnel != nil {
		_ = a.tunnel.Close()
		a.tunnel = nil
	}
	a.activeConn = nil
	// Drop cached columns -- belonged to the old schema.
	if a.columnCache != nil {
		a.columnCache.clear()
	}
	m := a.mainLayerPtr()
	m.resetResults()
	m.explorer.SetSchema(nil, db.SchemaDepthSchemas)
}

// loadSchema fetches the schema list from the active connection and hands it
// to the explorer. Called on successful connect and by the 'R' keybind in
// the Explorer panel. Errors are surfaced inside the explorer rather than
// the global status line so a transient schema failure doesn't swallow the
// last query's status.
func (a *app) loadSchema() {
	m := a.mainLayerPtr()
	if a.conn == nil {
		m.explorer.SetSchema(nil, db.SchemaDepthSchemas)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
	defer cancel()
	info, err := a.conn.Schema(ctx)
	if err != nil {
		m.explorer.SetError(err.Error())
		return
	}
	m.explorer.SetSchema(info, a.conn.Capabilities().SchemaDepth)
}

// --- query execution -------------------------------------------------------

// runQuery is the user-facing entry point. It guards against common
// destructive typos (UPDATE/DELETE without WHERE, TRUNCATE, DROP) by
// pushing a confirmation layer; runQueryUnsafe then does the actual work
// once the user confirms (or immediately if nothing looks dangerous).
func (a *app) runQuery() {
	m := a.mainLayerPtr()
	sess := m.session
	if sess.running {
		return
	}
	if a.conn == nil {
		sess.status = "no connection: press space then c to connect"
		return
	}
	sql := strings.TrimSpace(sess.editor.buf.Text())
	if sql == "" {
		sess.status = "nothing to run"
		return
	}
	if findings := sqltok.UnsafeMutations(sql); len(findings) > 0 {
		a.pushLayer(newConfirmRunLayer(findings))
		return
	}
	a.runQueryUnsafe()
}

// runQueryUnsafe kicks off the current editor SQL on a background
// goroutine that streams rows into the table widget as they arrive.
// Cancelling the ctx (Ctrl+C) aborts in-flight queries at the driver
// level; closing the Rows cursor throws away any buffered rows the
// driver hasn't handed us yet. Skips the destructive-statement guard —
// call runQuery for the guarded path.
func (a *app) runQueryUnsafe() {
	m := a.mainLayerPtr()
	sess := m.session
	if sess.running {
		return
	}
	if a.conn == nil {
		sess.status = "no connection: press space then c to connect"
		return
	}
	sql := strings.TrimSpace(sess.editor.buf.Text())
	if sql == "" {
		sess.status = "nothing to run"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	sess.running = true
	sess.lastQuerySQL = sql
	sess.lastQueryStart = time.Now()
	sess.resetResults()
	sess.status = "running query…"
	start := sess.lastQueryStart

	firstTab := sess.results[0]
	conn := a.conn
	resultCh := a.resultCh
	go func() {
		defer cancel()
		rows, err := conn.Query(ctx, sql)
		if err != nil {
			// Attach the error to the seeded placeholder tab so it renders
			// in the results pane (same path as mid-stream errors) rather
			// than only flashing on the status line.
			firstTab.table.Done(err)
			resultCh <- queryEvent{kind: evtResultSetDone, sess: sess, tab: firstTab, err: err, elapsed: time.Since(start)}
			resultCh <- queryEvent{kind: evtDone, sess: sess, err: err, elapsed: time.Since(start)}
			return
		}
		defer rows.Close()

		totalLoaded := 0
		setIdx := 0
		for {
			setIdx++
			var tab *resultTab
			if setIdx == 1 {
				// First set reuses the placeholder tab that resetResults
				// seeded on the main goroutine; main already knows about
				// it, so evtResultSetStart is informational.
				tab = firstTab
			} else {
				tab = newResultTab(nextResultTitle(setIdx))
			}
			tab.table.Init(rows.Columns())
			resultCh <- queryEvent{kind: evtResultSetStart, sess: sess, tab: tab, elapsed: time.Since(start)}

			loaded := 0
			lastReport := time.Now()
			capped := false
			capReason := ""
			for rows.Next() {
				row, scanErr := rows.Scan()
				if scanErr != nil {
					tab.table.Done(scanErr)
					resultCh <- queryEvent{kind: evtResultSetDone, sess: sess, tab: tab, err: scanErr, loaded: loaded, elapsed: time.Since(start)}
					resultCh <- queryEvent{kind: evtDone, sess: sess, err: scanErr, loaded: totalLoaded + loaded, elapsed: time.Since(start)}
					return
				}
				if !tab.table.Append(row) {
					capped = true
					capReason = tab.table.CapReason()
					break
				}
				loaded++
				if time.Since(lastReport) > progressThrottle {
					select {
					case resultCh <- queryEvent{kind: evtProgress, sess: sess, loaded: totalLoaded + loaded}:
					default:
					}
					lastReport = time.Now()
				}
			}
			if rerr := rows.Err(); rerr != nil {
				tab.table.Done(rerr)
				resultCh <- queryEvent{kind: evtResultSetDone, sess: sess, tab: tab, err: rerr, loaded: loaded, elapsed: time.Since(start)}
				resultCh <- queryEvent{kind: evtDone, sess: sess, err: rerr, loaded: totalLoaded + loaded, elapsed: time.Since(start)}
				return
			}
			tab.table.Done(nil)
			totalLoaded += loaded
			resultCh <- queryEvent{kind: evtResultSetDone, sess: sess, tab: tab, loaded: loaded, capped: capped, capReason: capReason, elapsed: time.Since(start)}

			if capped {
				break
			}
			if !rows.NextResultSet() {
				break
			}
		}
		resultCh <- queryEvent{kind: evtDone, sess: sess, loaded: totalLoaded, elapsed: time.Since(start)}
	}()
}

// cancelQuery aborts the in-flight query. Cancelling the context both
// stops any driver-side wait (pre-rows) and makes rows.Next() return
// false mid-stream; the goroutine's deferred rows.Close() then throws
// away any rows the driver had buffered ahead of us.
func (a *app) cancelQuery() {
	sess := a.mainLayerPtr().session
	if !sess.running || sess.cancel == nil {
		return
	}
	sess.cancel()
	sess.status = "cancelling…"
}

// handleQueryEvent updates the footer status as events arrive. The table
// widget is already being populated directly by the query goroutine, so
// this function only touches app.running / status text and, on evtDone,
// records the run in persistent history.
func (a *app) handleQueryEvent(e queryEvent) {
	sess := e.sess
	if sess == nil {
		return
	}
	switch e.kind {
	case evtResultSetStart:
		sess.status = "streaming…"
		if e.tab == nil {
			return
		}
		// First set reuses the placeholder tab already in results; any
		// subsequent set arrives as a new tab we append + activate so
		// the user sees rows streaming into it live.
		found := false
		for _, t := range sess.results {
			if t == e.tab {
				found = true
				break
			}
		}
		if !found {
			sess.appendResultTab(e.tab)
		}
	case evtProgress:
		sess.status = fmt.Sprintf("streaming… %d row(s)", e.loaded)
	case evtResultSetDone:
		if e.tab == nil {
			return
		}
		e.tab.lastRowCount = e.loaded
		e.tab.lastColCount = e.tab.table.ColCount()
		e.tab.lastElapsed = e.elapsed
		e.tab.lastCapped = e.capped
		e.tab.lastCapReason = e.capReason
		e.tab.lastHasResult = true
		e.tab.resultsErrScroll = 0
		if e.err != nil {
			if errors.Is(e.err, context.Canceled) {
				e.tab.lastErr = "cancelled"
			} else {
				e.tab.lastErr = e.err.Error()
				e.tab.lastErrLine = errinfo.Line(e.err, sess.lastQuerySQL)
			}
		} else {
			e.tab.lastErr = ""
			e.tab.lastErrLine = 0
		}
	case evtDone:
		sess.running = false
		sess.cancel = nil
		if e.err != nil {
			// Errors render in the active result tab's results pane via
			// the evtResultSetDone path. Keep the footer quiet on errors
			// so the user has one place to look, not two.
			if errors.Is(e.err, context.Canceled) {
				sess.status = fmt.Sprintf("cancelled after %d row(s)", e.loaded)
			} else {
				sess.status = ""
			}
		} else if len(sess.results) > 1 {
			sess.status = fmt.Sprintf("%d result set(s) / %d row(s) in %s",
				len(sess.results), e.loaded, e.elapsed.Round(time.Millisecond))
		} else {
			sess.status = ""
		}
		a.recordHistory(sess, e)
	}
}

// recordHistory persists the just-finished query to the store's history
// table. Failures here are logged to the status line but never block the
// user -- history is a convenience, not a correctness requirement.
func (a *app) recordHistory(sess *session, e queryEvent) {
	if a.store == nil || sess.lastQuerySQL == "" {
		return
	}
	connName := ""
	if a.activeConn != nil {
		connName = a.activeConn.Name
	}
	entry := store.HistoryEntry{
		ConnectionName: connName,
		SQL:            sess.lastQuerySQL,
		ExecutedAt:     sess.lastQueryStart.UTC(),
		Elapsed:        e.elapsed,
		RowCount:       int64(e.loaded),
	}
	if e.err != nil && !errors.Is(e.err, context.Canceled) {
		entry.Error = e.err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeHistoryTimeout)
	defer cancel()
	if err := a.store.RecordHistory(ctx, entry); err != nil {
		sess.status += " (history: " + err.Error() + ")"
	}
}
