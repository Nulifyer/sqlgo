package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/clipboard"
	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// queryEventKind tags the lifecycle phase of a running query. A single
// query can emit any number of evtProgress updates between an evtStarted
// (or an immediate-failure evtDone) and its final evtDone.
type queryEventKind int

const (
	// evtStarted: Query() returned a cursor; the table has been Init()'d
	// and rows will start flowing in via the query goroutine.
	evtStarted queryEventKind = iota
	// evtProgress: periodic row-count update. Non-authoritative and
	// dropped if the main loop is busy -- the final count arrives in
	// evtDone.
	evtProgress
	// evtDone: the query has finished, either cleanly (err==nil) or with
	// an error (including context.Canceled on user cancel).
	evtDone
)

// queryEvent is posted on the result channel during a query's lifetime.
// Moving status updates through the same channel as the final result
// gives the main loop a single select-case for everything query-related.
type queryEvent struct {
	kind    queryEventKind
	loaded  int
	capped  bool
	err     error
	elapsed time.Duration
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

	// Async query.
	running        bool
	resultCh       chan queryEvent
	cancel         context.CancelFunc
	lastQuerySQL   string    // SQL of the most recently started query (for history)
	lastQueryStart time.Time // wall-clock start of that query

	quit bool
}

// refreshConnections re-reads the saved-connections list from the store
// into connCache. Called after every mutation (save, delete, rename) so
// the picker's next Draw sees the change.
func (a *app) refreshConnections() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

// Run takes over the terminal and runs until the user quits (Ctrl+Q) or an
// error occurs. The terminal is always restored before return.
func Run() error {
	t, err := openTerminal()
	if err != nil {
		return err
	}
	defer t.Restore()

	fmt.Fprint(os.Stdout, altScreenOn)
	fmt.Fprint(os.Stdout, cursorHide)
	defer func() {
		fmt.Fprint(os.Stdout, cursorShow)
		fmt.Fprint(os.Stdout, altScreenOff)
	}()

	sec := secret.System()
	secAvail := sec.Available()

	a := &app{
		term: t,
		scr:  newScreen(os.Stdout, t.width, t.height),
		// Buffer a handful of events so a fast-streaming query doesn't
		// stall the drain goroutine on a non-blocking progress send.
		resultCh:         make(chan queryEvent, 8),
		clipboard:        clipboard.System(),
		secrets:          sec,
		secretsAvailable: secAvail,
	}
	a.layers = []Layer{newMainLayer()}

	// Open the persistent store (connections, history) and migrate it.
	// Any pre-existing connections.json file in the config dir is
	// imported on first boot so users upgrading from the JSON-only build
	// keep their saved connections.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 10*time.Second)
	st, err := store.Open(bootCtx)
	cancelBoot()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	a.store = st

	bootCtx, cancelBoot = context.WithTimeout(context.Background(), 10*time.Second)
	if _, err := a.store.BootstrapFromLegacyConfig(bootCtx); err != nil {
		// Non-fatal: log to stderr and continue with whatever the store
		// has (possibly empty).
		fmt.Fprintf(os.Stderr, "sqlgo: legacy config import: %v\n", err)
	}
	cancelBoot()

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
	}()

	return a.loop()
}

func (a *app) loop() error {
	keys := newKeyReader(os.Stdin)
	keyCh := make(chan Key, 8)
	keyErrCh := make(chan error, 1)
	go func() {
		for {
			k, err := keys.Read()
			if err != nil {
				keyErrCh <- err
				close(keyCh)
				return
			}
			keyCh <- k
		}
	}()

	for !a.quit {
		if a.term.refreshSize() {
			a.scr.resize(a.term.width, a.term.height)
		}
		a.draw()
		if err := a.scr.flush(); err != nil {
			return fmt.Errorf("flush: %w", err)
		}

		select {
		case k, ok := <-keyCh:
			if !ok {
				if err := <-keyErrCh; err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("read key: %w", err)
				}
				return nil
			}
			a.handleKey(k)
		case e := <-a.resultCh:
			a.handleQueryEvent(e)
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

// handleKey sends the key to the topmost layer, with two global escape
// hatches: Ctrl+Q quits, and F8 opens the hidden key-debug overlay. F8
// is handled here (not in a layer) so it's reachable from any modal.
func (a *app) handleKey(k Key) {
	if k.Ctrl && k.Rune == 'q' {
		a.quit = true
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
			pl.setStatus("ssh tunnel: dialing...")
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
		pl.setStatus("connecting...")
		// Flush the status update before we block on Open so the user
		// sees feedback.
		a.draw()
		_ = a.scr.flush()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	m.table.Clear()
	m.lastHasResult = false
	m.lastErr = ""
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
	m := a.mainLayerPtr()
	m.table.Clear()
	m.explorer.SetSchema(nil, db.SchemaDepthSchemas)
	m.lastHasResult = false
	m.lastErr = ""
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := a.conn.Schema(ctx)
	if err != nil {
		m.explorer.SetError(err.Error())
		return
	}
	m.explorer.SetSchema(info, a.conn.Capabilities().SchemaDepth)
}

// --- query execution -------------------------------------------------------

// runQuery kicks off the current editor SQL on a background goroutine that
// streams rows into the table widget as they arrive. Cancelling the ctx
// (Ctrl+C) aborts in-flight queries at the driver level; closing the Rows
// cursor throws away any buffered rows the driver hasn't handed us yet.
func (a *app) runQuery() {
	m := a.mainLayerPtr()
	if a.running {
		return
	}
	if a.conn == nil {
		m.status = "no connection: press space then c to connect"
		return
	}
	sql := strings.TrimSpace(m.editor.buf.Text())
	if sql == "" {
		m.status = "nothing to run"
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.running = true
	a.lastQuerySQL = sql
	a.lastQueryStart = time.Now()
	m.table.Clear()
	m.lastHasResult = false
	m.lastErr = ""
	m.status = "running query..."
	start := a.lastQueryStart

	tbl := m.table
	conn := a.conn
	resultCh := a.resultCh
	go func() {
		defer cancel()
		rows, err := conn.Query(ctx, sql)
		if err != nil {
			resultCh <- queryEvent{kind: evtDone, err: err, elapsed: time.Since(start)}
			return
		}
		defer rows.Close()

		tbl.Init(rows.Columns())
		resultCh <- queryEvent{kind: evtStarted, elapsed: time.Since(start)}

		loaded := 0
		lastReport := time.Now()
		for rows.Next() {
			row, scanErr := rows.Scan()
			if scanErr != nil {
				tbl.Done(scanErr)
				resultCh <- queryEvent{kind: evtDone, err: scanErr, loaded: loaded, elapsed: time.Since(start)}
				return
			}
			if !tbl.Append(row) {
				tbl.Done(nil)
				resultCh <- queryEvent{kind: evtDone, loaded: loaded, capped: true, elapsed: time.Since(start)}
				return
			}
			loaded++
			// Non-blocking progress pings wake the main loop for a
			// redraw so the user watches rows stream in live. If the
			// channel is full we skip -- evtDone will carry the final
			// count anyway.
			if time.Since(lastReport) > 50*time.Millisecond {
				select {
				case resultCh <- queryEvent{kind: evtProgress, loaded: loaded}:
				default:
				}
				lastReport = time.Now()
			}
		}
		if err := rows.Err(); err != nil {
			tbl.Done(err)
			resultCh <- queryEvent{kind: evtDone, err: err, loaded: loaded, elapsed: time.Since(start)}
			return
		}
		tbl.Done(nil)
		resultCh <- queryEvent{kind: evtDone, loaded: loaded, elapsed: time.Since(start)}
	}()
}

// cancelQuery aborts the in-flight query. Cancelling the context both
// stops any driver-side wait (pre-rows) and makes rows.Next() return
// false mid-stream; the goroutine's deferred rows.Close() then throws
// away any rows the driver had buffered ahead of us.
func (a *app) cancelQuery() {
	if !a.running || a.cancel == nil {
		return
	}
	a.cancel()
	a.mainLayerPtr().status = "cancelling..."
}

// handleQueryEvent updates the footer status as events arrive. The table
// widget is already being populated directly by the query goroutine, so
// this function only touches app.running / status text and, on evtDone,
// records the run in persistent history.
func (a *app) handleQueryEvent(e queryEvent) {
	m := a.mainLayerPtr()
	switch e.kind {
	case evtStarted:
		m.status = "streaming..."
	case evtProgress:
		m.status = fmt.Sprintf("streaming... %d row(s)", e.loaded)
	case evtDone:
		a.running = false
		a.cancel = nil
		m.lastRowCount = e.loaded
		m.lastElapsed = e.elapsed
		m.lastCapped = e.capped
		m.lastHasResult = true
		m.lastErr = ""
		if e.err != nil {
			if errors.Is(e.err, context.Canceled) {
				m.status = fmt.Sprintf("cancelled after %d row(s)", e.loaded)
				m.lastErr = "cancelled"
			} else if e.loaded > 0 {
				m.status = fmt.Sprintf("error after %d row(s): %s", e.loaded, e.err)
				m.lastErr = e.err.Error()
			} else {
				m.status = fmt.Sprintf("error: %s", e.err)
				m.lastErr = e.err.Error()
			}
		} else {
			suffix := ""
			if e.capped {
				suffix = " (buffer capped)"
			}
			m.status = fmt.Sprintf("%d row(s) in %s%s", e.loaded, e.elapsed.Round(time.Millisecond), suffix)
		}
		a.recordHistory(e)
	}
}

// recordHistory persists the just-finished query to the store's history
// table. Failures here are logged to the status line but never block the
// user -- history is a convenience, not a correctness requirement.
func (a *app) recordHistory(e queryEvent) {
	if a.store == nil || a.lastQuerySQL == "" {
		return
	}
	connName := ""
	if a.activeConn != nil {
		connName = a.activeConn.Name
	}
	entry := store.HistoryEntry{
		ConnectionName: connName,
		SQL:            a.lastQuerySQL,
		ExecutedAt:     a.lastQueryStart.UTC(),
		Elapsed:        e.elapsed,
		RowCount:       int64(e.loaded),
	}
	if e.err != nil && !errors.Is(e.err, context.Canceled) {
		entry.Error = e.err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.store.RecordHistory(ctx, entry); err != nil {
		// Append to status rather than overwriting -- the primary row
		// count / error is more important than the history failure.
		m := a.mainLayerPtr()
		m.status += " (history: " + err.Error() + ")"
	}
}
