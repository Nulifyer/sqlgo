package tui

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// ExportFormat is a serialization target for the results panel.
type ExportFormat int

const (
	ExportCSV ExportFormat = iota
	ExportTSV
	ExportJSON
	ExportMarkdown
)

// String returns the canonical name used in UI copy.
func (f ExportFormat) String() string {
	switch f {
	case ExportCSV:
		return "csv"
	case ExportTSV:
		return "tsv"
	case ExportJSON:
		return "json"
	case ExportMarkdown:
		return "markdown"
	}
	return "unknown"
}

// exportFormatFromPath picks a format from a filename's extension. Unknown
// extensions (including none) default to CSV so the path always produces
// a well-defined file. The ok flag lets the caller tell the user whether
// the extension was recognized.
func exportFormatFromPath(path string) (ExportFormat, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return ExportCSV, true
	case ".tsv", ".tab":
		return ExportTSV, true
	case ".json":
		return ExportJSON, true
	case ".md", ".markdown":
		return ExportMarkdown, true
	}
	return ExportCSV, false
}

// writeExport serializes a single result set (columns + already-stringified
// rows, the same buffer the table draws from) to w in the requested
// format. The rows slice can be empty; a file with just a header is
// valid output for every format.
func writeExport(w io.Writer, cols []db.Column, rows [][]string, format ExportFormat) error {
	switch format {
	case ExportCSV:
		return writeDelimited(w, cols, rows, ',')
	case ExportTSV:
		return writeDelimited(w, cols, rows, '\t')
	case ExportJSON:
		return writeJSON(w, cols, rows)
	case ExportMarkdown:
		return writeMarkdown(w, cols, rows)
	}
	return fmt.Errorf("export: unknown format %d", format)
}

func writeDelimited(w io.Writer, cols []db.Column, rows [][]string, comma rune) error {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("export header: %w", err)
	}
	for i, row := range rows {
		// Pad short rows defensively so ragged buffers (e.g. from a
		// mid-stream error) don't break the writer.
		if len(row) < len(cols) {
			padded := make([]string, len(cols))
			copy(padded, row)
			row = padded
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("export row %d: %w", i, err)
		}
	}
	cw.Flush()
	return cw.Error()
}

// jsonRow mirrors a single result row as an ordered-key object. encoding/
// json's Marshal on map[string]string doesn't preserve column order, so
// we use a struct-of-slice approach: each record is a []keyValue that
// Marshals as a JSON object by hand-rolling the writer.
type jsonResult struct {
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

func writeJSON(w io.Writer, cols []db.Column, rows [][]string) error {
	// We lose column ordering when using map[string]string, so the
	// top-level "columns" slice preserves the header order alongside
	// the record map. JSON consumers that care about order can read it
	// from there; the rows stay ergonomic for quick inspection.
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	out := jsonResult{
		Columns: names,
		Rows:    make([]map[string]string, 0, len(rows)),
	}
	for _, row := range rows {
		rec := make(map[string]string, len(cols))
		for i, c := range cols {
			if i < len(row) {
				rec[c.Name] = row[i]
			} else {
				rec[c.Name] = ""
			}
		}
		out.Rows = append(out.Rows, rec)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&out); err != nil {
		return fmt.Errorf("export json: %w", err)
	}
	return nil
}

func writeMarkdown(w io.Writer, cols []db.Column, rows [][]string) error {
	if len(cols) == 0 {
		return nil
	}
	bw := &strings.Builder{}

	bw.WriteString("|")
	for _, c := range cols {
		bw.WriteString(" ")
		bw.WriteString(escapeMarkdownCell(c.Name))
		bw.WriteString(" |")
	}
	bw.WriteString("\n|")
	for range cols {
		bw.WriteString(" --- |")
	}
	bw.WriteString("\n")

	for _, row := range rows {
		bw.WriteString("|")
		for i := range cols {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			bw.WriteString(" ")
			bw.WriteString(escapeMarkdownCell(cell))
			bw.WriteString(" |")
		}
		bw.WriteString("\n")
	}

	if _, err := io.WriteString(w, bw.String()); err != nil {
		return fmt.Errorf("export markdown: %w", err)
	}
	return nil
}

// escapeMarkdownCell turns a raw cell string into something that won't
// break the GFM pipe-table format: embedded newlines become "<br>",
// literal pipes are backslash-escaped, and leading/trailing whitespace
// is trimmed so cells align visually in a plain-text viewer.
func escapeMarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.TrimSpace(s)
}
