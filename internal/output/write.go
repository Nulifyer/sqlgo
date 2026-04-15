package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// Write serializes a single result set to w in the requested format.
// rows can be empty; a header-only file is valid output for every
// format. Delegates to WriteWith with zero Options so the formats
// that don't need them (everything except MarkdownQuery / SQLInsert)
// keep their original one-line call site.
func Write(w io.Writer, cols []db.Column, rows [][]string, format Format) error {
	return WriteWith(w, cols, rows, format, Options{})
}

// WriteWith is the long form that lets callers pass format-specific
// parameters. Formats that don't need Options ignore them.
func WriteWith(w io.Writer, cols []db.Column, rows [][]string, format Format, opts Options) error {
	switch format {
	case CSV:
		return writeDelimited(w, cols, rows, ',')
	case TSV:
		return writeDelimited(w, cols, rows, '\t')
	case JSON:
		return writeJSON(w, cols, rows)
	case Markdown:
		return writeMarkdown(w, cols, rows)
	case JSONL:
		return writeJSONL(w, cols, rows)
	case Table:
		return writeTable(w, cols, rows)
	case MarkdownQuery:
		return writeMarkdownQuery(w, cols, rows, opts.Query)
	case SQLInsert:
		return writeSQLInsert(w, cols, rows, opts.TableName)
	case HTML:
		return writeHTML(w, cols, rows)
	}
	return fmt.Errorf("output: unknown format %d", format)
}

func writeJSONL(w io.Writer, cols []db.Column, rows [][]string) error {
	enc := json.NewEncoder(w)
	for i, row := range rows {
		rec := make(map[string]string, len(cols))
		for j, c := range cols {
			if j < len(row) {
				rec[c.Name] = row[j]
			} else {
				rec[c.Name] = ""
			}
		}
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("output jsonl row %d: %w", i, err)
		}
	}
	return nil
}

func writeTable(w io.Writer, cols []db.Column, rows [][]string) error {
	if len(cols) == 0 {
		return nil
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = runeLen(c.Name)
	}
	for _, row := range rows {
		for i := range cols {
			if i < len(row) {
				if n := runeLen(row[i]); n > widths[i] {
					widths[i] = n
				}
			}
		}
	}
	var b strings.Builder
	writeTableRow := func(cells []string) {
		for i := range cols {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", widths[i]-runeLen(cell)))
		}
		b.WriteByte('\n')
	}
	writeTableRow(columnNames(cols))
	sep := make([]string, len(cols))
	for i, wd := range widths {
		sep[i] = strings.Repeat("-", wd)
	}
	writeTableRow(sep)
	for _, row := range rows {
		writeTableRow(row)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("output table: %w", err)
	}
	return nil
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func writeDelimited(w io.Writer, cols []db.Column, rows [][]string, comma rune) error {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	if err := cw.Write(columnNames(cols)); err != nil {
		return fmt.Errorf("output header: %w", err)
	}
	for i, row := range rows {
		if err := cw.Write(padRow(row, len(cols))); err != nil {
			return fmt.Errorf("output row %d: %w", i, err)
		}
	}
	cw.Flush()
	return cw.Error()
}

// jsonResult is the on-disk JSON shape. The top-level "columns" slice
// preserves header order alongside the per-row map (encoding/json doesn't
// preserve map key order), so consumers that care about column order can
// read it from there while the rows stay ergonomic for inspection.
type jsonResult struct {
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

func writeJSON(w io.Writer, cols []db.Column, rows [][]string) error {
	out := jsonResult{
		Columns: columnNames(cols),
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
		return fmt.Errorf("output json: %w", err)
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
		return fmt.Errorf("output markdown: %w", err)
	}
	return nil
}

// writeMarkdownQuery emits the originating SQL inside a ```sql fenced
// code block followed by the standard Markdown table. An empty query
// is skipped so the output is still a well-formed Markdown document.
func writeMarkdownQuery(w io.Writer, cols []db.Column, rows [][]string, query string) error {
	if query != "" {
		q := strings.TrimRight(query, "\r\n")
		if _, err := io.WriteString(w, "```sql\n"+q+"\n```\n\n"); err != nil {
			return fmt.Errorf("output markdown+query: %w", err)
		}
	}
	return writeMarkdown(w, cols, rows)
}

// writeSQLInsert emits one INSERT statement per row. Identifiers are
// double-quoted (ANSI) since the target dialect isn't known here; the
// values are single-quoted with embedded quotes doubled. NULL literals
// are not reconstructible from the string row buffer -- callers who
// need true NULLs should export to JSON/JSONL and re-import through a
// typed loader.
func writeSQLInsert(w io.Writer, cols []db.Column, rows [][]string, table string) error {
	if table == "" {
		table = "results"
	}
	if len(cols) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("INSERT INTO \"")
	b.WriteString(strings.ReplaceAll(table, "\"", "\"\""))
	b.WriteString("\" (")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(c.Name, "\"", "\"\""))
		b.WriteByte('"')
	}
	b.WriteString(") VALUES ")
	header := b.String()

	var out strings.Builder
	for _, row := range rows {
		out.WriteString(header)
		out.WriteByte('(')
		for i := range cols {
			if i > 0 {
				out.WriteString(", ")
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			out.WriteByte('\'')
			out.WriteString(strings.ReplaceAll(cell, "'", "''"))
			out.WriteByte('\'')
		}
		out.WriteString(");\n")
	}
	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("output sql: %w", err)
	}
	return nil
}

// writeHTML emits a minimal standalone document with a single <table>.
// No CSS -- consumers paste it into a styled report or an email; any
// structural tweaks belong in the enclosing template, not here.
func writeHTML(w io.Writer, cols []db.Column, rows [][]string) error {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>results</title></head><body>\n<table>\n<thead><tr>")
	for _, c := range cols {
		b.WriteString("<th>")
		b.WriteString(escapeHTML(c.Name))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for _, row := range rows {
		b.WriteString("<tr>")
		for i := range cols {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString("<td>")
			b.WriteString(escapeHTML(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody>\n</table>\n</body></html>\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("output html: %w", err)
	}
	return nil
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// escapeMarkdownCell turns a raw cell into something safe for the GFM
// pipe-table format: embedded newlines become "<br>", literal pipes are
// backslash-escaped, and surrounding whitespace is trimmed so cells
// align in a plain-text viewer.
func escapeMarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.TrimSpace(s)
}
