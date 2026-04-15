// Package output writes a result set (columns + already-stringified rows)
// to an io.Writer in one of several formats. It exists outside internal/tui
// so the TUI export overlay and the CLI exec/export verbs share a single
// implementation.
//
// The row buffer matches what the TUI's table widget snapshots: ragged
// rows are tolerated (short rows are padded with empty strings) so a
// mid-stream error can still flush a partial result without panicking.
package output

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/db"
)

// Format is a serialization target.
type Format int

const (
	CSV Format = iota
	TSV
	JSON
	Markdown
	// JSONL is newline-delimited JSON: one object per row, no outer
	// array or metadata. Streams cleanly for pipelines and big exports
	// where the consumer can't afford to buffer the whole set.
	JSONL
	// Table is an aligned plain-text grid for humans on a tty. Buffers
	// the full result to compute column widths, so it is not suitable
	// for streaming large exports.
	Table
	// MarkdownQuery writes the source SQL inside a ```sql code block
	// followed by the Markdown table. Useful for pasting result
	// snapshots into PRs/notebooks with the originating query.
	// Requires Options.Query.
	MarkdownQuery
	// SQLInsert writes INSERT statements for each row, quoted for the
	// target dialect (defaults to ANSI double quotes). Uses
	// Options.TableName, falling back to "results".
	SQLInsert
	// HTML writes a minimal standalone <table> document. Good for
	// emailing or embedding in a report viewer.
	HTML
)

// String returns the canonical name used in UI copy and CLI flags.
func (f Format) String() string {
	switch f {
	case CSV:
		return "csv"
	case TSV:
		return "tsv"
	case JSON:
		return "json"
	case Markdown:
		return "markdown"
	case JSONL:
		return "jsonl"
	case Table:
		return "table"
	case MarkdownQuery:
		return "markdown+query"
	case SQLInsert:
		return "sql"
	case HTML:
		return "html"
	}
	return "unknown"
}

// Options carries format-specific parameters that don't fit in the row
// buffer. Fields are only consulted by the formats that need them
// (Query by MarkdownQuery; TableName by SQLInsert) and are otherwise
// ignored, so callers can pass a single Options struct regardless of
// the chosen format.
type Options struct {
	// Query is the SQL that produced the rows. Emitted inside a ```sql
	// code block above the table when format == MarkdownQuery.
	Query string
	// TableName is the identifier used in the INSERT statement when
	// format == SQLInsert. Empty means "results".
	TableName string
}

// FormatFromPath picks a format from a filename's extension. Unknown
// extensions (including none) default to CSV so the path always produces
// well-defined output. The ok flag tells the caller whether the
// extension was recognized so it can warn the user when it wasn't.
func FormatFromPath(path string) (Format, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return CSV, true
	case ".tsv", ".tab":
		return TSV, true
	case ".json":
		return JSON, true
	case ".md", ".markdown":
		return Markdown, true
	case ".jsonl", ".ndjson":
		return JSONL, true
	case ".sql":
		return SQLInsert, true
	case ".html", ".htm":
		return HTML, true
	}
	return CSV, false
}

// FormatFromName parses a CLI --format value. Returns an error for
// unknown names so the CLI can refuse with a clear message rather than
// silently picking a default.
func FormatFromName(name string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "csv":
		return CSV, nil
	case "tsv":
		return TSV, nil
	case "json":
		return JSON, nil
	case "md", "markdown":
		return Markdown, nil
	case "jsonl", "ndjson":
		return JSONL, nil
	case "table":
		return Table, nil
	case "markdown+query", "md+query", "md-query":
		return MarkdownQuery, nil
	case "sql", "insert":
		return SQLInsert, nil
	case "html":
		return HTML, nil
	}
	return 0, fmt.Errorf("unknown format %q", name)
}

// DefaultExt returns the filename extension (including the leading
// dot) conventionally associated with f. Used by the export UI to
// auto-sync the path extension when the user cycles formats.
func (f Format) DefaultExt() string {
	switch f {
	case CSV:
		return ".csv"
	case TSV:
		return ".tsv"
	case JSON:
		return ".json"
	case JSONL:
		return ".jsonl"
	case Markdown, MarkdownQuery:
		return ".md"
	case SQLInsert:
		return ".sql"
	case HTML:
		return ".html"
	case Table:
		return ".txt"
	}
	return ""
}

// columnNames extracts the header slice once so each writer doesn't
// re-walk cols.
func columnNames(cols []db.Column) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

// padRow returns row padded with empty strings to len(cols). Returns the
// original row when no padding is needed to avoid the allocation.
func padRow(row []string, n int) []string {
	if len(row) >= n {
		return row
	}
	out := make([]string, n)
	copy(out, row)
	return out
}
