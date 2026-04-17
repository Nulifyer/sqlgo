package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/store"
	"github.com/Nulifyer/sqlgo/internal/tui/widget"
)

// exportFormats is the ordered list shown by the in-layer picker.
// Kept short and opinionated: the formats a human actually exports
// results to from a TUI. "table" is omitted (the TUI already displays
// the grid) and so is JSONL's JSON (JSONL subsumes the common use).
// Hardcoded order = stable keybind muscle memory.
var exportFormats = []output.Format{
	output.CSV,
	output.TSV,
	output.Markdown,
	output.MarkdownQuery,
	output.JSON,
	output.JSONL,
	output.SQLInsert,
	output.HTML,
}

// exportLayer is the modal overlay that prompts for an export path and
// writes the current results buffer to disk. Directory + filename +
// format are all driven by widget.FilePicker in ModeSaveTarget, with the
// Format row always visible since exportFormats has eight entries.
type exportLayer struct {
	picker *widget.FilePicker
	// query / tableName are snapshotted at construction so a mid-edit
	// to the query editor doesn't change what gets written to the
	// MarkdownQuery / SQLInsert outputs.
	query     string
	tableName string
	// busy is set while a goroutine is writing the file. Input and
	// Enter are ignored during this window so the user can't kick off a
	// second export or dismiss the layer mid-write (Esc still cancels
	// the UI, but the write keeps running -- that's fine, it's local
	// disk).
	busy     bool
	frame    string
	status   string
	initDone bool
}

// newExportLayer builds a layer seeded with a timestamped default name,
// cwd as the initial dir, and the session's current query text.
func newExportLayer(a *app) *exportLayer {
	dir := seedDir(a, store.LastDirExport)
	stem := "results-" + time.Now().Format("2006-01-02-T-15-04-05")

	choices := make([]widget.ExtChoice, len(exportFormats))
	for i, f := range exportFormats {
		choices[i] = widget.ExtChoice{Ext: f.DefaultExt(), Label: f.String()}
	}
	fp := widget.NewFilePicker(widget.FilePickerOpts{
		Mode:       widget.ModeSaveTarget,
		Dir:        dir,
		Name:       stem,
		Choices:    choices,
		ShowFormat: true,
	})

	el := &exportLayer{
		picker:    fp,
		tableName: "results",
	}
	if m := a.mainLayerPtr(); m != nil && m.session != nil {
		el.query = m.session.lastQuerySQL
	}
	return el
}

// triggerScan is the OnDirChange callback wired on first Draw. Mirrors
// the save_layer pattern: list one directory level on a goroutine, post
// back via asyncCh, discard when the layer has been popped.
func (el *exportLayer) triggerScan(a *app) {
	base := el.picker.ScanBase()
	go func() {
		rows, err := widget.ListDir(base)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		a.asyncCh <- func(app *app) {
			if top, ok := app.topLayer().(*exportLayer); !ok || top != el {
				return
			}
			el.picker.ApplyRows(base, rows, errStr)
		}
	}()
}

func (el *exportLayer) Draw(a *app, c *cellbuf) {
	if !el.initDone {
		el.initDone = true
		el.picker.SetOnDirChange(func() { el.triggerScan(a) })
		el.picker.NotifyDirChange()
	}

	boxW := 80
	if boxW > a.term.width-dialogMargin {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 50 {
		boxW = 50
	}
	boxH := el.picker.PreferredHeight(10)
	if boxH > a.term.height-dialogMargin {
		boxH = a.term.height - dialogMargin
	}
	if boxH < 13 {
		boxH = 13
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
	widget.DrawDialog(c, r, "Export results", true)

	el.picker.Draw(c, r, widget.DrawOpts{
		FocusedFG: colorBorderFocused,
		DimStyle:  Style{FG: ansiBrightBlack, BG: ansiDefaultBG},
		Truncate:  truncate,
	})

	innerCol := r.Col + 2
	innerW := r.W - 4

	rows := 0
	if a.layers != nil {
		rows = a.mainLayerPtr().table.RowCount()
	}
	rowInfo := fmt.Sprintf("Rows: %d", rows)

	status := el.status
	if el.busy {
		status = "writing " + el.frame
	}
	statusLine := rowInfo
	if status != "" {
		statusLine = rowInfo + "   " + status
	}
	c.SetFg(colorBorderFocused)
	c.WriteAt(r.Row+r.H-2, innerCol, truncate(statusLine, innerW))
	c.ResetStyle()
}

func (el *exportLayer) HandleKey(a *app, k Key) {
	if el.busy {
		// Only Esc is honored while the write is running. It pops the
		// layer but the goroutine keeps going; its completion posts a
		// status back to the main view instead of to this layer.
		if k.Kind == KeyEsc {
			a.popLayer()
		}
		return
	}
	if k.Kind == KeyEsc {
		a.popLayer()
		return
	}
	res := el.picker.HandleKey(k)
	if res.SaveRequested {
		el.save(a)
	}
}

func (el *exportLayer) save(a *app) {
	m := a.mainLayerPtr()
	if !m.table.HasColumns() {
		el.status = "no results to export"
		return
	}

	path := el.picker.Path()
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		el.status = "directory does not exist: " + parent
		return
	}

	// Overwrite guard: two-Enter pattern via picker's shared guard.
	if !el.picker.Guard.Check(path) {
		el.status = "file exists -- Enter again to overwrite"
		return
	}

	cols, rows := m.table.Snapshot()
	opts := output.Options{Query: el.query, TableName: el.tableName}
	format := exportFormats[el.picker.ExtIdx]

	el.busy = true
	el.frame = spinnerFrames[0]
	el.status = ""

	done := make(chan struct{})
	go runSpinner(a, done, func(a *app, frame string) {
		if top, ok := a.topLayer().(*exportLayer); ok && top == el {
			el.frame = frame
		}
	})
	go func() {
		writeErr := writeExportFile(path, cols, rows, format, opts)
		close(done)
		a.asyncCh <- func(a *app) {
			el.busy = false
			if writeErr != nil {
				el.status = writeErr.Error()
				return
			}
			recordDir(a, store.LastDirExport, filepath.Dir(path))
			// If the user Esc'd the layer mid-write, post the success
			// onto the main view instead so they still see it.
			msg := fmt.Sprintf("exported %d row(s) to %s", len(rows), path)
			if top, ok := a.topLayer().(*exportLayer); ok && top == el {
				a.popLayer()
			}
			a.mainLayerPtr().status = msg
		}
	}()
}

// writeExportFile runs on a goroutine; all error wrapping happens here
// so the main-loop callback only has to stringify.
func writeExportFile(path string, cols []db.Column, rows [][]string, format output.Format, opts output.Options) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create failed: %w", err)
	}
	if err := output.WriteWith(f, cols, rows, format, opts); err != nil {
		_ = f.Close()
		return fmt.Errorf("write failed: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close failed: %w", err)
	}
	return nil
}

func (el *exportLayer) Hints(a *app) string {
	_ = a
	if el.picker.Guard.Armed() {
		return joinHints("↵=overwrite", "edit=cancel", "Esc=close")
	}
	switch el.picker.Focus {
	case widget.FocusDir:
		return joinHints("type=dir", "⇥=next", "↵=descend", "Esc=cancel")
	case widget.FocusList:
		has := el.picker.HasEntries()
		return joinHints(
			hintIf(has, "↑/↓=move"),
			"⇥=next",
			"↵=pick",
			"Esc=cancel",
		)
	case widget.FocusInput:
		return joinHints("type=name", "⇥=next", "↵=save", "Esc=cancel")
	case widget.FocusExt:
		return joinHints("↑/↓=format", "⇥=next", "Esc=cancel")
	}
	return joinHints("⇥=next", "↵=save", "Esc=cancel")
}
