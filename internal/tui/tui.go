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
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/aliases"
	_ "github.com/Nulifyer/sqlgo/internal/db/athena"
	_ "github.com/Nulifyer/sqlgo/internal/db/azuresql"
	_ "github.com/Nulifyer/sqlgo/internal/db/bigquery"
	_ "github.com/Nulifyer/sqlgo/internal/db/clickhouse"
	_ "github.com/Nulifyer/sqlgo/internal/db/d1"
	_ "github.com/Nulifyer/sqlgo/internal/db/databricks"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	_ "github.com/Nulifyer/sqlgo/internal/db/file"
	_ "github.com/Nulifyer/sqlgo/internal/db/firebird"
	_ "github.com/Nulifyer/sqlgo/internal/db/flightsql"
	_ "github.com/Nulifyer/sqlgo/internal/db/hana"
	_ "github.com/Nulifyer/sqlgo/internal/db/libsql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/oracle"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/snowflake"
	_ "github.com/Nulifyer/sqlgo/internal/db/spanner"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
	_ "github.com/Nulifyer/sqlgo/internal/db/sybase"
	_ "github.com/Nulifyer/sqlgo/internal/db/trino"
	_ "github.com/Nulifyer/sqlgo/internal/db/vertica"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"github.com/Nulifyer/sqlgo/internal/store"
	tuiterm "github.com/Nulifyer/sqlgo/internal/tui/term"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
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
	runGen    uint64
	loaded    int
	capped    bool
	capReason string
	err       error
	errInfo   errinfo.Info
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

	// documents owns file-backed query-tab invariants shared by open /
	// save flows.
	documents documentService

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
	connOps    connCoordinator
	connGen    uint64
	// tunnel is the SSH jump connection, if the active connection
	// routes through one. Closed in disconnect() after the db.Conn,
	// so any lingering reads on the forwarded socket get the right
	// "driver closed" error first.
	tunnel *sshtunnel.Tunnel

	// Async query. The resultCh is a single pump shared across sessions;
	// per-session runner state (running/cancel/lastQuery*/lastQueryStart)
	// lives on *session so parallel tabs don't fight over one cancel handle.
	resultCh chan queryEvent

	// asyncCh carries callbacks from background goroutines back to the
	// main event loop. Used for non-query background work (schema fetch,
	// history record, etc.) so the UI never blocks on IO. Callbacks run
	// on the main goroutine and can safely touch UI state.
	asyncCh chan func(*app)
	doneCh  chan struct{}

	// columnCache memoizes editor autocomplete lookups.
	// Cleared on disconnect so fresh schema wins.
	columnCache *columnCache

	// Connection and schema generations fence asynchronous work. A newer
	// connection attempt cancels its predecessor; schema state belongs to
	// exactly one installed connection generation.
	connectGen    uint64
	connectCancel context.CancelFunc
	schemaLoading bool
	schemaGen     uint64
	schemaCancel  context.CancelFunc

	// metadataBusy is set while a broad metadata operation, such as
	// explorer deep-search column loading, is using the active Conn. The
	// Conn contract requires serialized use, so query execution and other
	// fresh metadata fetches wait until this clears.
	metadataBusy bool

	quit bool

	version       string
	updateVersion string
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

// resolvePassword returns the real password for a saved connection,
// delegating to secret.Resolve so the CLI can share the same fetch
// logic. Errors are wrapped with the connection name so the user sees
// exactly which connect failed and why.
func (a *app) resolvePassword(c config.Connection) (string, error) {
	pass, err := secret.Resolve(a.secrets, c.Name, c.Password)
	if err != nil {
		return "", fmt.Errorf("password for %q: %w", c.Name, err)
	}
	return pass, nil
}

type keyringChange struct {
	account  string
	previous string
	existed  bool
}

// keyringChanges records enough state to undo a sequence of keyring writes.
// A rollback restores overwritten credentials and deletes only accounts that
// did not exist before the save attempt.
type keyringChanges struct {
	store   secret.Store
	changes []keyringChange
}

func (k *keyringChanges) set(account, value string) error {
	previous, err := k.store.Get(account)
	existed := true
	if err != nil {
		if !secret.IsNotFound(err) {
			return fmt.Errorf("read prior keyring account %q: %w", account, err)
		}
		existed = false
	}
	if err := k.store.Set(account, value); err != nil {
		return err
	}
	k.changes = append(k.changes, keyringChange{
		account: account, previous: previous, existed: existed,
	})
	return nil
}

func (k *keyringChanges) rollback() error {
	var rollbackErr error
	for i := len(k.changes) - 1; i >= 0; i-- {
		change := k.changes[i]
		var err error
		if change.existed {
			err = k.store.Set(change.account, change.previous)
		} else {
			err = k.store.Delete(change.account)
		}
		if err != nil {
			rollbackErr = errors.Join(rollbackErr,
				fmt.Errorf("restore keyring account %q: %w", change.account, err))
		}
	}
	return rollbackErr
}

func rollbackKeyring(changes *keyringChanges, cause error) error {
	return errors.Join(cause, changes.rollback())
}

// persistConnection upserts a connection via the store and, when the
// OS keyring is available, also rewrites the row to use the sqlgo
// keyring placeholder so the plaintext password never lands on disk.
// oldName carries any pre-rename identifier so the store's atomic
// rename path is used.
//
// A new database password may still fall back to plaintext when no keyring is
// available. An SSH password entered while editing a keyring-backed database
// connection does not: persisting that form fails instead of silently writing
// the new SSH secret to sqlite. The ok return lets callers surface the storage
// choice exactly once per save.
func (a *app) persistConnection(ctx context.Context, oldName string, c config.Connection) (usedKeyring bool, err error) {
	// A placeholder DB password means that secret is already in the
	// keyring, but an edited form may still contain a newly entered SSH
	// password. Move that SSH secret before saving the row; the previous
	// early return wrote it to sqlite as plaintext.
	if c.Password == secret.Placeholder {
		rowCopy := c
		changes := &keyringChanges{store: a.secrets}
		if rowCopy.SSH.Password != "" && rowCopy.SSH.Password != secret.Placeholder {
			if !a.secretsAvailable || a.secrets == nil {
				return false, fmt.Errorf("store SSH password: keyring unavailable")
			}
			account := connectutil.SSHKeyringAccount(c.Name)
			if err := changes.set(account, rowCopy.SSH.Password); err != nil {
				return false, fmt.Errorf("store SSH password in keyring: %w", err)
			}
			rowCopy.SSH.Password = secret.Placeholder
		}
		if err := a.store.SaveConnection(ctx, oldName, rowCopy); err != nil {
			return false, rollbackKeyring(changes, err)
		}
		return true, nil
	}

	if a.secretsAvailable && a.secrets != nil {
		changes := &keyringChanges{store: a.secrets}
		// Best-effort: write the secret first, then the row with the
		// placeholder. If the secret write fails, fall back to
		// plaintext rather than losing the password entirely.
		if werr := changes.set(c.Name, c.Password); werr == nil {
			rowCopy := c
			rowCopy.Password = secret.Placeholder

			// SSH password: mirror the same fallback-and-warn flow so
			// the jump-host secret never lands on disk when we have
			// a keyring. Empty SSH passwords and placeholder values
			// (edit path with no change) are left alone.
			if rowCopy.SSH.Password != "" && rowCopy.SSH.Password != secret.Placeholder {
				if err := changes.set(connectutil.SSHKeyringAccount(c.Name), rowCopy.SSH.Password); err != nil {
					cause := fmt.Errorf("store SSH password in keyring: %w", err)
					return false, rollbackKeyring(changes, cause)
				}
				rowCopy.SSH.Password = secret.Placeholder
			}

			if sErr := a.store.SaveConnection(ctx, oldName, rowCopy); sErr != nil {
				return false, rollbackKeyring(changes, sErr)
			}
			// If this was a rename, delete the old keyring entries.
			if oldName != "" && oldName != c.Name {
				_ = a.secrets.Delete(oldName)
				_ = a.secrets.Delete(connectutil.SSHKeyringAccount(oldName))
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
		_ = a.secrets.Delete(connectutil.SSHKeyringAccount(name))
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
	_ = a.secrets.Delete(connectutil.SSHKeyringAccount(name))

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
	// Version is set only by release builds. Empty disables automatic checks.
	Version string

	// InitialQuery, if non-empty, seeds the query editor with this text
	// on startup. Used by the CLI `sqlgo file.sql` entry point.
	InitialQuery string

	// InitialConnection, if non-nil, auto-connects to this entry on
	// startup -- picker is pushed first so a connect failure still
	// leaves the user with a usable UI, then connectTo runs and pops it
	// on success. Used by `sqlgo open FILE` to drop the user straight
	// into an ephemeral file-driver session without touching the store.
	InitialConnection *config.Connection
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
		version: opts.Version,
		term:    t,
		scr:     newScreen(os.Stdout, t.width, t.height),
		// Buffer a handful of events so a fast-streaming query doesn't
		// stall the drain goroutine on a non-blocking progress send.
		resultCh:         make(chan queryEvent, resultChanBuf),
		asyncCh:          make(chan func(*app), asyncChanBuf),
		doneCh:           make(chan struct{}),
		clipboard:        clipboard.System(),
		secrets:          sec,
		secretsAvailable: secAvail,
	}
	ml := newMainLayer()
	if opts.InitialQuery != "" {
		ml.ensureActiveTab().editor.buf.SetText(opts.InitialQuery)
		ml.editor.ClearErrorLocation()
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

	// Ephemeral auto-connect path (e.g. `sqlgo open file.csv`). Queue
	// onto asyncCh so the connect runs *after* loop() has entered the
	// alt-screen and set up terminal modes -- otherwise connectTo's
	// status-line draw/flush spills control sequences over the user's
	// shell history. Picker stays the fallback on connect failure.
	if opts.InitialConnection != nil {
		ic := *opts.InitialConnection
		a.asyncCh <- func(a *app) { a.connectTo(ic) }
	}

	defer func() {
		a.cancelConnectAttempt()
		a.invalidateSchemaLoad()
		a.stopAllQueries()
		release := a.connOps.acquire()
		defer release()
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
		a.scr.TeardownView()
	}()

	stopUpdates := a.beginUpdateCheck()
	defer stopUpdates()
	loopErr := a.loop()
	close(a.doneCh)
	return loopErr
}

func (a *app) loop() error {
	keys := newKeyReader(tuiterm.StdinReader())
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
			a.scr.Resize(a.term.width, a.term.height)
		}
		// Apply declarative terminal modes (alt-screen, mouse, paste,
		// title) before the cell-diff flush so the next diff lands in
		// the right buffer.
		if err := a.scr.ApplyView(a.effectiveView()); err != nil {
			return fmt.Errorf("apply view: %w", err)
		}
		a.draw()
		if err := a.scr.Flush(); err != nil {
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
		case fn := <-a.asyncCh:
			fn(a)
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
	if a.term.width < 40 || a.term.height < 10 {
		b := a.scr.LayerBuf(0)
		msg := "terminal too small (need 40x10)"
		row := a.term.height / 2
		if row < 1 {
			row = 1
		}
		col := (a.term.width-displayWidth(msg))/2 + 1
		if col < 1 {
			col = 1
		}
		b.WriteAt(row, col, truncate(msg, a.term.width))
		a.scr.Composite([]*cellbuf{b})
		return
	}
	bufs := make([]*cellbuf, len(a.layers))
	for i, l := range a.layers {
		b := a.scr.LayerBuf(i)
		l.Draw(a, b)
		bufs[i] = b
	}
	a.scr.Composite(bufs)
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
		a.requestQuit()
		return
	}
	if k.Kind == KeyF1 {
		if _, ok := a.topLayer().(*helpLayer); ok {
			a.popLayer()
			return
		}
		a.pushLayer(newAppHelpLayer(a))
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
	a.connectGen++
	gen := a.connectGen
	if a.connectCancel != nil {
		a.connectCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	a.connectCancel = cancel

	pl, _ := a.topLayer().(*pickerLayer)
	runtime := connectutil.DefaultRuntimeDeps(a.secrets)
	resolved, err := connectutil.ResolveSavedConnection(c, runtime)
	if err != nil {
		cancel()
		if a.connectGen == gen {
			a.connectCancel = nil
		}
		if pl != nil {
			pl.setStatus(err.Error())
		}
		return
	}
	if resolved.Tunnel != nil {
		if pl != nil {
			pl.setStatus("ssh tunnel: dialing…")
			a.draw()
			_ = a.scr.Flush()
		}
	}

	// File-driver CSV/JSONL ingestion can take many seconds on large
	// inputs, so d.Open runs on a goroutine and a ticker pushes spinner
	// frames through asyncCh until it finishes. Without this, the main
	// loop is blocked and the picker looks frozen on "connecting…".
	if pl != nil {
		pl.setStatus("connecting " + spinnerFrames[0])
	}

	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		if pl, ok := a.topLayer().(*pickerLayer); ok {
			pl.setStatus("connecting " + frame)
		}
	})
	go func() {
		defer cancel()
		conn, tunnel, err := connectutil.OpenResolvedConnection(ctx, resolved, runtime)
		close(done)
		select {
		case a.asyncCh <- func(a *app) {
			a.finishConnect(gen, c, conn, tunnel, err)
		}:
		case <-a.doneCh:
			if conn != nil {
				_ = conn.Close()
			}
			if tunnel != nil {
				_ = tunnel.Close()
			}
		}
	}()
}

func (a *app) cancelConnectAttempt() {
	a.connectGen++
	if a.connectCancel != nil {
		a.connectCancel()
		a.connectCancel = nil
	}
}

// spinnerFrames re-exports widget.SpinnerFrames so existing call sites
// that seed an initial glyph (e.g. spinnerFrames[0]) keep compiling.
var spinnerFrames = widget.SpinnerFrames

// runSpinner is the app-level adapter for widget.RunSpinner: it wraps
// apply in a non-blocking asyncCh send so all UI mutation stays on
// the main goroutine. Dropping a frame under backpressure is fine --
// the next tick paints anyway -- and avoids stalling the goroutine
// which would delay the final "done" apply from the caller.
func runSpinner(a *app, done <-chan struct{}, apply func(a *app, frame string)) {
	widget.RunSpinner(done, func(frame string) {
		select {
		case a.asyncCh <- func(a *app) { apply(a, frame) }:
		default:
		}
	})
}

// finishConnect runs on the main loop after d.Open returns. It applies
// the same commit/teardown logic the original synchronous connectTo
// used, but now has to tolerate the picker having been dismissed or
// swapped in the meantime.
func (a *app) finishConnect(gen uint64, c config.Connection, conn db.Conn, tunnel *sshtunnel.Tunnel, err error) {
	if gen != a.connectGen {
		if conn != nil {
			_ = conn.Close()
		}
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return
	}
	a.connectCancel = nil
	pl, _ := a.topLayer().(*pickerLayer)
	if err != nil {
		if tunnel != nil {
			_ = tunnel.Close()
		}
		var unknown *sshtunnel.UnknownHostError
		if errors.As(err, &unknown) {
			a.pushLayer(newTrustLayer(c, unknown))
			return
		}
		var mismatch *sshtunnel.HostKeyMismatchError
		if errors.As(err, &mismatch) {
			if pl != nil {
				pl.setStatus(mismatch.Error())
			}
			return
		}
		if pl != nil {
			pl.setStatus("connect failed: " + err.Error())
		}
		return
	}

	oldConn, oldTunnel := a.conn, a.tunnel
	a.stopAllQueries()
	a.invalidateSchemaLoad()
	if a.columnCache != nil {
		a.columnCache.clear()
	}
	a.connGen++
	a.conn = conn
	a.tunnel = tunnel
	a.metadataBusy = false
	cc := c
	a.activeConn = &cc
	a.connErr = nil

	// Only pop if the picker is still on top -- user may have dismissed it.
	if _, ok := a.topLayer().(*pickerLayer); ok {
		a.popLayer()
	}
	m := a.mainLayerPtr()
	m.status = "connected"
	m.editor.ClearErrorLocation()
	m.resetResults()
	m.explorer.ResetDatabases()
	for _, s := range m.sessions {
		s.activeCatalog = ""
	}
	m.focus = FocusExplorer
	a.retireConnection(oldConn, oldTunnel)
	a.loadSchema()
}

func (a *app) stopAllQueries() {
	m := a.mainLayerPtr()
	if m != nil {
		for _, s := range m.sessions {
			s.runGen++
			if s.running && s.cancel != nil {
				s.cancel()
			}
			s.running = false
			s.runnerDone = nil
			s.runnerFrame = ""
			s.cancel = nil
		}
	}
}

func (a *app) invalidateSchemaLoad() {
	if a.schemaCancel != nil {
		a.schemaCancel()
		a.schemaCancel = nil
	}
	a.schemaLoading = false
	a.schemaGen++
}

func (a *app) retireConnection(conn db.Conn, tunnel *sshtunnel.Tunnel) {
	if conn == nil && tunnel == nil {
		return
	}
	go func() {
		release := a.connOps.acquire()
		defer release()
		if conn != nil {
			_ = conn.Close()
		}
		if tunnel != nil {
			_ = tunnel.Close()
		}
	}()
}

func (a *app) disconnect() {
	if a.conn == nil {
		return
	}
	m := a.mainLayerPtr()
	oldConn, oldTunnel := a.conn, a.tunnel
	a.stopAllQueries()
	a.invalidateSchemaLoad()
	a.connGen++
	a.conn = nil
	a.metadataBusy = false
	// Close the tunnel after the db conn so any lingering reads on
	// the forwarded socket get the driver's "closed" error first.
	a.tunnel = nil
	a.activeConn = nil
	// Drop cached columns -- belonged to the old schema.
	if a.columnCache != nil {
		a.columnCache.clear()
	}
	if m != nil {
		m.resetResults()
		m.explorer.SetSchema(nil, db.SchemaDepthSchemas)
		m.explorer.ResetDatabases()
		// Clear every tab's pinned catalog -- the old server is gone; a
		// re-connect to a different server must not reuse stale DB names.
		for _, s := range m.sessions {
			s.activeCatalog = ""
		}
	}
	a.retireConnection(oldConn, oldTunnel)
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
	if a.schemaLoading {
		return
	}
	a.schemaLoading = true
	a.schemaGen++
	schemaGen := a.schemaGen
	connGen := a.connGen
	// Capture the current conn so a disconnect+reconnect during the
	// fetch can't apply stale schema to the new connection.
	conn := a.conn
	caps := conn.Capabilities()
	depth := caps.SchemaDepth

	// Cross-DB drivers opened with a blank default database list
	// databases at the root; each DB's schema is fetched lazily on
	// expand via loadDatabaseSchema.
	if caps.SupportsCrossDatabase && a.activeConn != nil && a.activeConn.Database == "" {
		lister, ok := conn.(db.DatabaseLister)
		if ok {
			m.explorer.SetLoading()
			done := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
			a.schemaCancel = cancel
			go runSpinner(a, done, func(a *app, frame string) {
				if a.conn != conn {
					return
				}
				a.mainLayerPtr().explorer.SetLoadingFrame(frame)
			})
			go func() {
				release := a.connOps.acquire()
				names, err := lister.ListDatabases(ctx)
				release()
				cancel()
				close(done)
				a.asyncCh <- func(a *app) {
					if a.conn != conn || a.connGen != connGen || a.schemaGen != schemaGen {
						return
					}
					a.schemaLoading = false
					a.schemaCancel = nil
					m := a.mainLayerPtr()
					if err != nil {
						m.explorer.SetError(err.Error())
						return
					}
					m.explorer.depth = depth
					m.explorer.SetDatabases(names)
				}
			}()
			return
		}
	}

	m.explorer.SetLoading()
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
	a.schemaCancel = cancel
	go runSpinner(a, done, func(a *app, frame string) {
		// Only animate if this goroutine's fetch is still the active one
		// (conn unchanged). A stale tick on a swapped connection would
		// otherwise scribble a spinner frame over a fresh explorer state.
		if a.conn != conn {
			return
		}
		a.mainLayerPtr().explorer.SetLoadingFrame(frame)
	})
	go func() {
		release := a.connOps.acquire()
		info, err := conn.Schema(ctx)
		release()
		cancel()
		close(done)
		a.asyncCh <- func(a *app) {
			if a.conn != conn || a.connGen != connGen || a.schemaGen != schemaGen {
				return
			}
			a.schemaLoading = false
			a.schemaCancel = nil
			m := a.mainLayerPtr()
			if err != nil {
				m.explorer.SetError(err.Error())
				return
			}
			m.explorer.SetSchema(info, depth)
		}
	}()
}

// loadDatabaseSchema fetches one database's schema via the driver's
// DatabaseLister and hands it to the explorer. Called when the user
// expands a DB row in cross-DB mode. No-op if a fetch for the same
// catalog is already in flight (dbLoading marker set).
func (a *app) loadDatabaseSchema(catalog string) {
	if a.conn == nil || catalog == "" {
		return
	}
	lister, ok := a.conn.(db.DatabaseLister)
	if !ok {
		return
	}
	conn := a.conn
	connGen := a.connGen
	schemaGen := a.schemaGen
	m := a.mainLayerPtr()
	m.explorer.SetDatabaseLoading(catalog, spinnerFrames[0])

	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		if a.conn != conn {
			return
		}
		a.mainLayerPtr().explorer.SetDatabaseLoadingFrame(catalog, frame)
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), schemaTimeout)
		defer cancel()
		release := a.connOps.acquire()
		info, err := lister.SchemaForDatabase(ctx, catalog)
		release()
		close(done)
		a.asyncCh <- func(a *app) {
			if a.conn != conn || a.connGen != connGen || a.schemaGen != schemaGen {
				return
			}
			m := a.mainLayerPtr()
			if err != nil {
				m.explorer.SetDatabaseError(catalog, err.Error())
				return
			}
			m.explorer.SetDatabaseSchema(catalog, info)
		}
	}()
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
		sess.status = "no connection: press Ctrl+K then c to connect"
		return
	}
	if a.metadataBusy {
		sess.status = "metadata loading; try again when it finishes"
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
// catalogPreamble returns the `USE [db]` statement to prepend to the next
// batch for sess, or "" if no routing is needed. Empty when: no active
// connection, driver is single-DB, the login hard-coded a default DB
// (connection already routes there), or the tab has no pinned catalog.
func (a *app) catalogPreamble(sess *session) string {
	if sess == nil || sess.activeCatalog == "" || a.conn == nil {
		return ""
	}
	if a.activeConn != nil && a.activeConn.Database != "" {
		return ""
	}
	if !a.conn.Capabilities().SupportsCrossDatabase {
		return ""
	}
	lister, ok := a.conn.(db.DatabaseLister)
	if !ok {
		return ""
	}
	return lister.UseDatabaseStmt(sess.activeCatalog)
}

func (a *app) runQueryUnsafe() {
	m := a.mainLayerPtr()
	sess := m.session
	if sess.running {
		return
	}
	if a.conn == nil {
		sess.status = "no connection: press Ctrl+K then c to connect"
		return
	}
	bufferText := sess.editor.buf.Text()
	sql := strings.TrimSpace(bufferText)
	if sql == "" {
		sess.status = "nothing to run"
		return
	}
	sentSQL := sql
	preambleLines := 0
	if pre := a.catalogPreamble(sess); pre != "" {
		sentSQL = pre + "\n" + sql
		preambleLines = strings.Count(pre, "\n") + 1
	}
	if sess.preview {
		sess.preview = false
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	sess.running = true
	sess.runGen++
	runGen := sess.runGen
	sess.runnerFrame = spinnerFrames[0]
	runnerDone := make(chan struct{})
	sess.runnerDone = runnerDone
	sess.lastQueryDriver = a.conn.Driver()
	sess.lastQueryBufferText = bufferText
	sess.lastQuerySQL = sql
	sess.lastQuerySentSQL = sentSQL
	sess.lastQueryPreambleLines = preambleLines
	sess.lastQueryStart = time.Now()
	sess.resetResults()
	sess.editor.ClearErrorLocation()
	sess.status = "running query…"
	go runSpinner(a, runnerDone, func(a *app, frame string) {
		m := a.mainLayerPtr()
		if m.session != sess || !sess.running {
			return
		}
		sess.runnerFrame = frame
	})
	start := sess.lastQueryStart

	firstTab := sess.results[0]
	conn := a.conn
	resultCh := a.resultCh
	go func() {
		defer cancel()
		defer close(runnerDone)
		send := func(e queryEvent) bool {
			e.runGen = runGen
			select {
			case resultCh <- e:
				return true
			case <-a.doneCh:
				return false
			}
		}
		release := a.connOps.acquire()
		defer release()
		rows, err := conn.Query(ctx, sentSQL)
		if err != nil {
			// Attach the error to the seeded placeholder tab so it renders
			// in the results pane (same path as mid-stream errors) rather
			// than only flashing on the status line.
			firstTab.table.Done(err)
			if !send(queryEvent{
				kind:    evtResultSetDone,
				sess:    sess,
				tab:     firstTab,
				err:     err,
				errInfo: db.ParseErrorInfo(conn.Driver(), err, sentSQL),
				elapsed: time.Since(start),
			}) {
				return
			}
			send(queryEvent{kind: evtDone, sess: sess, err: err, elapsed: time.Since(start)})
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
			if !send(queryEvent{kind: evtResultSetStart, sess: sess, tab: tab, elapsed: time.Since(start)}) {
				return
			}

			loaded := 0
			lastReport := time.Now()
			capped := false
			capReason := ""
			for rows.Next() {
				row, scanErr := rows.Scan()
				if scanErr != nil {
					tab.table.Done(scanErr)
					if !send(queryEvent{
						kind:    evtResultSetDone,
						sess:    sess,
						tab:     tab,
						err:     scanErr,
						errInfo: db.ParseErrorInfo(conn.Driver(), scanErr, sentSQL),
						loaded:  loaded,
						elapsed: time.Since(start),
					}) {
						return
					}
					send(queryEvent{kind: evtDone, sess: sess, err: scanErr, loaded: totalLoaded + loaded, elapsed: time.Since(start)})
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
					case resultCh <- queryEvent{kind: evtProgress, runGen: runGen, sess: sess, loaded: totalLoaded + loaded}:
					default:
					}
					lastReport = time.Now()
				}
			}
			if rerr := rows.Err(); rerr != nil {
				tab.table.Done(rerr)
				if !send(queryEvent{
					kind:    evtResultSetDone,
					sess:    sess,
					tab:     tab,
					err:     rerr,
					errInfo: db.ParseErrorInfo(conn.Driver(), rerr, sentSQL),
					loaded:  loaded,
					elapsed: time.Since(start),
				}) {
					return
				}
				send(queryEvent{kind: evtDone, sess: sess, err: rerr, loaded: totalLoaded + loaded, elapsed: time.Since(start)})
				return
			}
			tab.table.Done(nil)
			totalLoaded += loaded
			if !send(queryEvent{kind: evtResultSetDone, sess: sess, tab: tab, loaded: loaded, capped: capped, capReason: capReason, elapsed: time.Since(start)}) {
				return
			}

			if capped {
				break
			}
			if !rows.NextResultSet() {
				break
			}
		}
		send(queryEvent{kind: evtDone, sess: sess, loaded: totalLoaded, elapsed: time.Since(start)})
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
	if e.runGen != 0 && e.runGen != sess.runGen {
		return
	}
	switch e.kind {
	case evtResultSetStart:
		sess.status = "streaming…"
		if e.tab == nil {
			return
		}
		// First set reuses the placeholder tab already in results; any
		// subsequent set arrives as a new tab we append. The active
		// tab stays on Result 1 so focus doesn't jump around.
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
				e.tab.lastErrLine = 0
				e.tab.lastErrCol = 0
				sess.editor.ClearErrorLocation()
			} else {
				info := e.errInfo
				if info.Message == "" {
					info = db.ParseErrorInfo(sess.lastQueryDriver, e.err, sess.lastQuerySentSQL)
				}
				e.tab.lastErr = info.Format()
				loc := info.Location.ShiftLines(-sess.lastQueryPreambleLines)
				e.tab.lastErrLine = loc.Line
				e.tab.lastErrCol = loc.Column
				if loc.Line > 0 && sess.editor.buf.Text() == sess.lastQueryBufferText {
					sess.editor.SetErrorLocation(loc.Line, loc.Column)
				} else {
					sess.editor.ClearErrorLocation()
				}
			}
		} else {
			e.tab.lastErr = ""
			e.tab.lastErrLine = 0
			e.tab.lastErrCol = 0
			sess.editor.ClearErrorLocation()
		}
	case evtDone:
		sess.running = false
		sess.runnerDone = nil
		sess.runnerFrame = ""
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
	store := a.store
	ch := a.asyncCh
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), storeHistoryTimeout)
		defer cancel()
		if err := store.RecordHistory(ctx, entry); err != nil {
			msg := " (history: " + err.Error() + ")"
			select {
			case ch <- func(*app) { sess.status += msg }:
			default:
			}
		}
	}()
}
