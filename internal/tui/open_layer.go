package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/store"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// openLayer is the modal overlay that loads a .sql file into the editor.
// It's a thin wrapper around widget.FilePicker in ModeOpenMulti: the
// picker owns the Dir field, browse list, Find input, marks, and mouse
// hit-test. This layer dispatches the single-level ListDir scan, drives
// the scan spinner, and resolves Enter into either a single-tab load or
// a marked batch load.
type openLayer struct {
	picker *widget.FilePicker
	clicks clickTracker

	status    string
	scanning  bool
	scanFrame string

	// initDone is flipped on first Draw so the picker's OnDirChange
	// binds to *app exactly once and the initial scan fires.
	initDone bool
}

func newOpenLayer(a *app, seed string) *openLayer {
	dir := seedDir(a, store.LastDirOpen)
	fp := widget.NewFilePicker(widget.FilePickerOpts{
		Mode: widget.ModeOpenMulti,
		Dir:  dir,
		Exts: []string{".sql"},
	})
	if seed != "" {
		fp.SetSearch(seed)
	}
	ol := &openLayer{
		picker:    fp,
		scanning:  true,
		scanFrame: spinnerFrames[0],
	}
	ol.status = ol.scanFrame + " scanning for .sql files…"
	return ol
}

// triggerScan is the OnDirChange callback wired on first Draw. Lists
// one directory level on a goroutine, posts back via asyncCh with a
// staleness fence so results from a superseded base are dropped. A
// spinner runs alongside so the status line stays live.
func (ol *openLayer) triggerScan(a *app) {
	base := ol.picker.ScanBase()

	ol.scanning = true
	ol.scanFrame = spinnerFrames[0]
	ol.status = ol.scanFrame + " scanning for .sql files…"
	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		if top, ok := a.topLayer().(*openLayer); ok && top == ol && ol.scanning {
			ol.scanFrame = frame
			ol.status = frame + " scanning for .sql files…"
		}
	})
	go func() {
		rows, err := widget.ListDir(base)
		close(done)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		a.asyncCh <- func(app *app) {
			if top, ok := app.topLayer().(*openLayer); !ok || top != ol {
				return
			}
			if ol.picker.ScanBase() != base {
				return
			}
			ol.scanning = false
			ol.picker.ApplyRows(base, rows, errStr)
			ol.refreshStatus()
		}
	}()
}

// fileCounts reports total files in the current directory and how many
// are visible after the Open-mode filter/search. Dir + parent rows are
// excluded from both counts so the status line matches user intent
// ("N of M files").
func (ol *openLayer) fileCounts() (total, visible int) {
	p := ol.picker
	for _, r := range p.Rows {
		if r.Kind == widget.RowFile {
			total++
		}
	}
	for _, i := range p.Filtered {
		if p.Rows[i].Kind == widget.RowFile {
			visible++
		}
	}
	return
}

// refreshStatus recomputes the status line from picker state. Called
// after any input / scan event that changes counts or marks.
func (ol *openLayer) refreshStatus() {
	p := ol.picker
	switch {
	case ol.scanning:
		ol.status = ol.scanFrame + " scanning for .sql files…"
	case p.ScanErr() != "":
		ol.status = p.ScanErr()
	default:
		total, visible := ol.fileCounts()
		switch {
		case total == 0:
			ol.status = "no .sql files in " + p.DirBase()
		case visible == 0:
			ol.status = "no matches"
		default:
			n := len(p.MarkedPaths())
			if n > 0 {
				ol.status = fmt.Sprintf("%d / %d files -- %d marked (Enter opens each in a new tab)", visible, total, n)
			} else {
				ol.status = fmt.Sprintf("%d / %d files", visible, total)
			}
		}
	}
}

func (ol *openLayer) Draw(a *app, c *cellbuf) {
	if !ol.initDone {
		ol.initDone = true
		ol.picker.SetOnDirChange(func() { ol.triggerScan(a) })
		ol.picker.NotifyDirChange()
	}

	boxW := 100
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := 22
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	if boxH < 10 {
		boxH = 10
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{Row: row, Col: col, W: boxW, H: boxH}
	widget.DrawDialog(c, r, "Open SQL file", true)

	ol.picker.Draw(c, r, widget.DrawOpts{
		FocusedFG: colorBorderFocused,
		DimStyle:  Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
		Truncate:  truncate,
	})

	if ol.status != "" {
		innerCol := r.Col + 2
		innerW := r.W - 4
		c.SetFg(colorStatusBar)
		c.WriteAt(r.Row+r.H-2, innerCol, truncate(ol.status, innerW))
		c.ResetStyle()
	}
}

func (ol *openLayer) HandleKey(a *app, k Key) {
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	res := ol.picker.HandleKey(k)
	if res.OpenRequested {
		ol.load(a)
		return
	}
	ol.refreshStatus()
}

// load resolves Enter into a load. Marked entries open each in a new
// tab; otherwise the highlighted entry loads into the current tab,
// falling back to treating the Find field's text as a direct path when
// no entries match.
func (ol *openLayer) load(a *app) {
	if marked := ol.picker.MarkedPaths(); len(marked) > 0 {
		ol.loadMarked(a, marked)
		return
	}
	var path string
	if e, ok := ol.picker.SelectedEntry(); ok {
		path = e.Abs
	} else {
		path = strings.TrimSpace(ol.picker.SearchText())
	}
	if path == "" {
		ol.status = "path is required"
		return
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = filepath.Clean(abs)
	}
	m := a.mainLayerPtr()
	if idx := m.findTabByPath(path); idx >= 0 {
		m.switchTab(idx)
		recordDir(a, store.LastDirOpen, filepath.Dir(path))
		a.popLayer()
		m.status = fmt.Sprintf("switched to open tab for %s", filepath.Base(path))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ol.status = "read failed: " + err.Error()
		return
	}
	text := string(data)
	sess := m.ensureActiveTab()
	sess.editor.buf.SetText(text)
	sess.editor.ClearErrorLocation()
	sess.sourcePath = path
	sess.savedText = text
	sess.title = filepath.Base(path)
	recordDir(a, store.LastDirOpen, filepath.Dir(path))
	a.popLayer()
	m.status = fmt.Sprintf("loaded %d bytes from %s", len(data), path)
}

// loadMarked opens each marked path in its own new editor tab. Files
// already open in another tab are switched to rather than re-opened;
// read failures are counted and surfaced on the main view.
func (ol *openLayer) loadMarked(a *app, marked []string) {
	m := a.mainLayerPtr()
	var loaded, skipped, reused int
	var lastErr, lastLoadedPath string
	lastIdx := -1
	for _, abs := range marked {
		if idx := m.findTabByPath(abs); idx >= 0 {
			reused++
			lastIdx = idx
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			skipped++
			lastErr = err.Error()
			continue
		}
		m.newTab()
		text := string(data)
		m.editor.buf.SetText(text)
		m.editor.ClearErrorLocation()
		m.session.sourcePath = abs
		m.session.savedText = text
		m.session.title = filepath.Base(abs)
		loaded++
		lastIdx = m.activeTab
		lastLoadedPath = abs
	}
	if loaded == 0 && reused == 0 {
		if lastErr != "" {
			ol.status = "read failed: " + lastErr
		} else {
			ol.status = "no files loaded"
		}
		return
	}
	if lastIdx >= 0 {
		m.switchTab(lastIdx)
	}
	if lastLoadedPath != "" {
		recordDir(a, store.LastDirOpen, filepath.Dir(lastLoadedPath))
	}
	a.popLayer()
	switch {
	case skipped > 0:
		m.status = fmt.Sprintf("opened %d new, %d already open (%d failed)", loaded, reused, skipped)
	case reused > 0:
		m.status = fmt.Sprintf("opened %d new, %d already open", loaded, reused)
	default:
		m.status = fmt.Sprintf("opened %d files in new tabs", loaded)
	}
}

// HandleInput delegates mouse events to the picker, then applies the
// click-tracker so a double-click triggers a load.
func (ol *openLayer) HandleInput(a *app, msg InputMsg) bool {
	mm, ok := msg.(MouseMsg)
	if !ok {
		return false
	}
	idx, consumed := ol.picker.HandleMouse(mm)
	if !consumed {
		return false
	}
	if idx >= 0 && mm.Button == MouseButtonLeft && mm.Action == MouseActionPress {
		count := ol.clicks.bump(mm)
		if count >= 2 {
			ol.load(a)
			return true
		}
	}
	ol.refreshStatus()
	return true
}

func (ol *openLayer) View(a *app) View {
	return View{AltScreen: true, MouseEnabled: true}
}

func (ol *openLayer) Hints(a *app) string {
	_ = a
	switch ol.picker.Focus {
	case widget.FocusDir:
		return joinHints("type=dir", "Tab=next", "Enter=descend", "Esc=cancel")
	case widget.FocusList:
		has := ol.picker.HasEntries()
		return joinHints(
			hintIf(has, "Up/Dn/PgUp/PgDn=move"),
			hintIf(has, "Space=mark"),
			"Tab=next",
			"Enter=open",
			"Esc=cancel",
		)
	case widget.FocusInput:
		return joinHints("type=filter", "Tab=next", "Enter=open", "Esc=cancel")
	}
	return joinHints("Tab=next", "Enter=open", "Esc=cancel")
}
