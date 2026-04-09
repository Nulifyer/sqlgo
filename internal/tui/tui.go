package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
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

	// Active connection.
	conn       db.Conn
	connErr    error
	activeConn *config.Connection

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

	a := &app{
		term: t,
		scr:  newScreen(os.Stdout, t.width, t.height),
		// Buffer a handful of events so a fast-streaming query doesn't
		// stall the drain goroutine on a non-blocking progress send.
		resultCh: make(chan queryEvent, 8),
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

// handleKey sends the key to the topmost layer, with a single global escape
// hatch for Ctrl+Q.
func (a *app) handleKey(k Key) {
	if k.Ctrl && k.Rune == 'q' {
		a.quit = true
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
	cfg := db.Config{
		Host:     c.Host,
		Port:     c.Port,
		User:     c.User,
		Password: c.Password,
		Database: c.Database,
		Options:  c.Options,
	}
	// Sensible MSSQL default: skip cert validation for dev containers.
	if c.Driver == "mssql" {
		if cfg.Options == nil {
			cfg.Options = map[string]string{}
		}
		if _, ok := cfg.Options["encrypt"]; !ok {
			cfg.Options["encrypt"] = "disable"
		}
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
		if pl != nil {
			pl.setStatus("connect failed: " + err.Error())
		}
		return
	}

	if a.conn != nil {
		_ = a.conn.Close()
	}
	a.conn = conn
	cc := c
	a.activeConn = &cc
	a.connErr = nil

	// Dismiss the picker and reset the main-view state.
	a.popLayer()
	m := a.mainLayerPtr()
	m.status = "connected"
	m.table.Clear()
	a.loadSchema()
}

func (a *app) disconnect() {
	if a.conn == nil {
		return
	}
	_ = a.conn.Close()
	a.conn = nil
	a.activeConn = nil
	m := a.mainLayerPtr()
	m.table.Clear()
	m.explorer.SetSchema(nil)
}

// loadSchema fetches the schema list from the active connection and hands it
// to the explorer. Called on successful connect and by the 'R' keybind in
// the Explorer panel. Errors are surfaced inside the explorer rather than
// the global status line so a transient schema failure doesn't swallow the
// last query's status.
func (a *app) loadSchema() {
	m := a.mainLayerPtr()
	if a.conn == nil {
		m.explorer.SetSchema(nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := a.conn.Schema(ctx)
	if err != nil {
		m.explorer.SetError(err.Error())
		return
	}
	m.explorer.SetSchema(info)
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
		if e.err != nil {
			if errors.Is(e.err, context.Canceled) {
				m.status = fmt.Sprintf("cancelled after %d row(s)", e.loaded)
			} else if e.loaded > 0 {
				m.status = fmt.Sprintf("error after %d row(s): %s", e.loaded, e.err)
			} else {
				m.status = fmt.Sprintf("error: %s", e.err)
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
