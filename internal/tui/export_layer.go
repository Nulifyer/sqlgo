package tui

import (
	"fmt"
	"os"
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
	if f, ok := exportFormatFromPath(el.path.String()); ok {
		fmtName = f.String()
	}
	c.writeAt(cur+4, innerCol, truncate("Format:     "+fmtName, boxW-4))

	rows := 0
	if a.layers != nil {
		rows = a.mainLayerPtr().table.RowCount()
	}
	c.writeAt(cur+5, innerCol, truncate(fmt.Sprintf("Rows to export: %d", rows), boxW-4))

	if el.status != "" {
		c.setFg(colorBorderFocused)
		c.writeAt(r.row+r.h-2, innerCol, truncate(el.status, boxW-4))
		c.resetStyle()
	}
}

func (el *exportLayer) HandleKey(a *app, k Key) {
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

	format, known := exportFormatFromPath(path)
	cols, rows := m.table.Snapshot()

	f, err := os.Create(path)
	if err != nil {
		el.status = "create failed: " + err.Error()
		return
	}
	if err := writeExport(f, cols, rows, format); err != nil {
		_ = f.Close()
		el.status = "write failed: " + err.Error()
		return
	}
	if err := f.Close(); err != nil {
		el.status = "close failed: " + err.Error()
		return
	}

	a.popLayer()
	if known {
		m.status = fmt.Sprintf("exported %d row(s) to %s", len(rows), path)
	} else {
		m.status = fmt.Sprintf("exported %d row(s) to %s (unknown extension, used csv)", len(rows), path)
	}
}

func (el *exportLayer) Hints(a *app) string {
	_ = a
	return joinHints("type=path", "Enter=save", "Esc=cancel")
}
