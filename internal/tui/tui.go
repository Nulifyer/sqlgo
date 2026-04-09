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
)

// queryResult is posted on the result channel when a background query finishes.
type queryResult struct {
	res     *db.Result
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

	// Config / saved connections.
	confFile *config.File

	// Active connection.
	conn       db.Conn
	connErr    error
	activeConn *config.Connection

	// Async query.
	running  bool
	resultCh chan queryResult
	cancel   context.CancelFunc

	quit bool
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
		term:     t,
		scr:      newScreen(os.Stdout, t.width, t.height),
		resultCh: make(chan queryResult, 1),
	}
	a.layers = []Layer{newMainLayer()}

	cf, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	a.confFile = cf

	// Start on the picker so the user picks or creates a connection first.
	a.pushLayer(newPickerLayer(cf.Connections))

	defer func() {
		if a.conn != nil {
			_ = a.conn.Close()
		}
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
		case r := <-a.resultCh:
			a.handleQueryResult(r)
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
	m.table.SetResult(nil)
}

func (a *app) disconnect() {
	if a.conn == nil {
		return
	}
	_ = a.conn.Close()
	a.conn = nil
	a.activeConn = nil
	a.mainLayerPtr().table.SetResult(nil)
}

// --- query execution -------------------------------------------------------

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
	m.status = "running query..."
	start := time.Now()
	go func() {
		res, err := a.conn.Query(ctx, sql)
		a.resultCh <- queryResult{res: res, err: err, elapsed: time.Since(start)}
	}()
}

func (a *app) cancelQuery() {
	if !a.running || a.cancel == nil {
		return
	}
	a.cancel()
	a.mainLayerPtr().status = "cancelling..."
}

func (a *app) handleQueryResult(r queryResult) {
	a.running = false
	a.cancel = nil
	m := a.mainLayerPtr()
	if r.err != nil {
		m.status = fmt.Sprintf("error: %s", r.err)
		return
	}
	rows := 0
	if r.res != nil {
		rows = len(r.res.Rows)
	}
	m.status = fmt.Sprintf("%d row(s) in %s", rows, r.elapsed.Round(time.Millisecond))
	m.table.SetResult(r.res)
}
