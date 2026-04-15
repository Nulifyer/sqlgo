package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
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
// writes the current results buffer to disk. The format is chosen via
// an in-layer picker (Tab / Shift+Tab) which keeps the path extension
// in sync; FormatFromPath is still used as the initial guess when the
// user hand-edits the extension directly.
//
// Kept as its own layer (rather than wedging it into the command menu)
// because it owns a live input field and a transient status display.
type exportLayer struct {
	path   *input
	format output.Format
	status string
	// query / tableName are snapshotted at construction so a mid-edit
	// to the query editor doesn't change what gets written to the
	// MarkdownQuery / SQLInsert outputs.
	query     string
	tableName string
	// confirmOverwrite is set after the first Enter finds an existing
	// file. A second Enter (with the path unchanged) proceeds with the
	// write; any edit to the path clears the flag so the user re-confirms
	// against the new path.
	confirmOverwrite bool
	confirmedPath    string
	// busy is set while a goroutine is writing the file. Input and
	// Enter are ignored during this window so the user can't kick off a
	// second export or dismiss the layer mid-write (Esc still cancels
	// the UI, but the write keeps running -- that's fine, it's local
	// disk).
	busy  bool
	frame string
}

// newExportLayer builds a layer seeded with a timestamped default path
// and the session's current query text. The seed extension follows the
// initial format (CSV) and is auto-synced when the user cycles formats.
func newExportLayer(a *app) *exportLayer {
	format := output.CSV
	stamp := time.Now().Format("2006-01-02-T-15-04-05")
	seed := "results-" + stamp + format.DefaultExt()

	el := &exportLayer{
		path:      newInput(seed),
		format:    format,
		tableName: "results",
	}
	if m := a.mainLayerPtr(); m != nil && m.session != nil {
		el.query = m.session.lastQuerySQL
	}
	return el
}

// syncExtension rewrites the path's extension to match the current
// format, preserving the stem the user typed. If the user deleted the
// extension entirely we still append the new one so the file lands
// with a recognizable suffix.
func (el *exportLayer) syncExtension() {
	p := el.path.String()
	if p == "" {
		return
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	el.path.SetString(stem + el.format.DefaultExt())
}

func (el *exportLayer) Draw(a *app, c *cellbuf) {
	boxW := 64
	if boxW > a.term.width-4 {
		boxW = a.term.width - dialogMargin
	}
	if boxW < 40 {
		boxW = 40
	}
	boxH := 11
	if boxH > a.term.height-4 {
		boxH = a.term.height - dialogMargin
	}
	row := (a.term.height - boxH) / 2
	col := (a.term.width - boxW) / 2
	if row < 1 {
		row = 1
	}
	if col < 1 {
		col = 1
	}
	r := rect{row: row, col: col, w: boxW, h: boxH}
	c.fillRect(r)
	drawFrame(c, r, "Export results", true)

	innerCol := col + 2
	cur := row + 1

	c.writeAt(cur+1, innerCol, truncate("Path:", boxW-4))
	// Value display with cursor.
	valCol := innerCol + 7
	maxVal := boxW - 7 - 4
	if maxVal < 1 {
		maxVal = 1
	}
	val := el.path.String()
	rs := []rune(val)
	if len(rs) > maxVal {
		rs = rs[len(rs)-maxVal:]
	}
	c.writeAt(cur+1, valCol, string(rs))
	c.placeCursor(cur+1, valCol+len(rs))

	c.writeAt(cur+3, innerCol, truncate("Format: "+el.format.String(), boxW-4))

	rows := 0
	if a.layers != nil {
		rows = a.mainLayerPtr().table.RowCount()
	}
	c.writeAt(cur+5, innerCol, truncate(fmt.Sprintf("Rows to export: %d", rows), boxW-4))

	status := el.status
	if el.busy {
		status = "writing " + el.frame
	}
	if status != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-3, innerCol, truncate(status, boxW-4))
		c.resetStyle()
	}

	// Hint pinned to the bottom row, dim so it reads as chrome rather
	// than content.
	hintStyle := Style{FG: ansiBrightBlack, BG: ansiDefaultBG}
	c.writeStyled(r.row+r.h-2, innerCol, truncate("Tab/Shift+Tab=format  Enter=save  Esc=cancel", boxW-4), hintStyle)
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
	if k.Kind == KeyTab {
		el.cycleFormat(false)
		return
	}
	if k.Kind == KeyBackTab {
		el.cycleFormat(true)
		return
	}
	if k.Kind == KeyEnter {
		el.save(a)
		return
	}
	before := el.path.String()
	el.path.handle(k)
	if el.path.String() != before {
		// Any path edit invalidates the overwrite confirmation so the
		// user has to reconfirm for the new target.
		el.confirmOverwrite = false
		el.confirmedPath = ""
	}
}

// cycleFormat advances (or rewinds with shift) the picker and rewrites
// the path's extension to match.
func (el *exportLayer) cycleFormat(back bool) {
	idx := 0
	for i, f := range exportFormats {
		if f == el.format {
			idx = i
			break
		}
	}
	if back {
		idx = (idx - 1 + len(exportFormats)) % len(exportFormats)
	} else {
		idx = (idx + 1) % len(exportFormats)
	}
	el.format = exportFormats[idx]
	el.syncExtension()
	el.confirmOverwrite = false
	el.confirmedPath = ""
	el.status = ""
}

func (el *exportLayer) save(a *app) {
	path := el.path.String()
	if path == "" {
		el.status = "path is required"
		return
	}

	m := a.mainLayerPtr()
	if !m.table.HasColumns() {
		el.status = "no results to export"
		return
	}

	// Overwrite guard: two-Enter pattern. Path must match the one that
	// triggered the first prompt, otherwise the user edited between
	// presses and should reconfirm.
	if _, err := os.Stat(path); err == nil {
		if !el.confirmOverwrite || el.confirmedPath != path {
			el.confirmOverwrite = true
			el.confirmedPath = path
			el.status = "file exists -- Enter again to overwrite"
			return
		}
	}

	cols, rows := m.table.Snapshot()
	opts := output.Options{Query: el.query, TableName: el.tableName}
	format := el.format

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
	return joinHints("type=path", "Tab=format", "Enter=save", "Esc=cancel")
}
