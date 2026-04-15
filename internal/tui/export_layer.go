package tui

import (
	"fmt"
	"os"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
)

// exportLayer is the modal overlay that prompts for an export path and
// writes the current results buffer to disk. Format is inferred from the
// path's extension (csv/tsv/json/md); unknown extensions fall back to
// CSV with a warning in the status line.
//
// Kept as its own layer (rather than wedging it into the space menu)
// because it owns a live input field and a transient status display.
type exportLayer struct {
	path   *input
	status string
	// busy is set while a goroutine is writing the file. Input and
	// Enter are ignored during this window so the user can't kick off a
	// second export or dismiss the layer mid-write (Esc still cancels
	// the UI, but the write keeps running -- that's fine, it's local
	// disk).
	busy  bool
	frame string
}

func newExportLayer(seed string) *exportLayer {
	return &exportLayer{path: newInput(seed)}
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

	// Format hint: tell the user what extensions are recognized and
	// which one their current path would produce.
	c.writeAt(cur+3, innerCol, truncate("Extensions: .csv  .tsv  .json  .md", boxW-4))
	fmtName := "csv (default)"
	if f, ok := output.FormatFromPath(el.path.String()); ok {
		fmtName = f.String()
	}
	c.writeAt(cur+4, innerCol, truncate("Format:     "+fmtName, boxW-4))

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
		c.writeAt(r.row+r.h-2, innerCol, truncate(status, boxW-4))
		c.resetStyle()
	}
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
	if k.Kind == KeyEnter {
		el.save(a)
		return
	}
	// Delegate editing to the embedded input.
	el.path.handle(k)
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

	format, known := output.FormatFromPath(path)
	cols, rows := m.table.Snapshot()

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
		writeErr := writeExportFile(path, cols, rows, format)
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
			if !known {
				msg += " (unknown extension, used csv)"
			}
			if top, ok := a.topLayer().(*exportLayer); ok && top == el {
				a.popLayer()
			}
			a.mainLayerPtr().status = msg
		}
	}()
}

// writeExportFile runs on a goroutine; all error wrapping happens here
// so the main-loop callback only has to stringify.
func writeExportFile(path string, cols []db.Column, rows [][]string, format output.Format) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create failed: %w", err)
	}
	if err := output.Write(f, cols, rows, format); err != nil {
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
	return joinHints("type=path", "Enter=save", "Esc=cancel")
}
