package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

var exportCols = []db.Column{{Name: "id"}, {Name: "name"}}
var exportRows = [][]string{
	{"1", "alice"},
	{"2", "bo|b"},            // pipe in markdown must be escaped
	{"3", "line1\nline2"},    // newline in markdown must become <br>
}

func TestExportFormatFromPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		want  ExportFormat
		known bool
	}{
		{"out.csv", ExportCSV, true},
		{"out.tsv", ExportTSV, true},
		{"out.json", ExportJSON, true},
		{"out.md", ExportMarkdown, true},
		{"out.markdown", ExportMarkdown, true},
		{"out.txt", ExportCSV, false}, // default
		{"out", ExportCSV, false},
	}
	for _, tc := range cases {
		got, known := exportFormatFromPath(tc.path)
		if got != tc.want || known != tc.known {
			t.Errorf("exportFormatFromPath(%q) = (%v,%v), want (%v,%v)", tc.path, got, known, tc.want, tc.known)
		}
	}
}

func TestWriteCSV(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeExport(&buf, exportCols, exportRows, ExportCSV); err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	want := "id,name\n1,alice\n2,bo|b\n3,\"line1\nline2\"\n"
	if buf.String() != want {
		t.Errorf("csv =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestWriteTSV(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeExport(&buf, exportCols, exportRows, ExportTSV); err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	// encoding/csv uses \r\n line endings by default regardless of delimiter.
	if !strings.Contains(buf.String(), "id\tname") {
		t.Errorf("tsv missing header: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "1\talice") {
		t.Errorf("tsv missing first row: %q", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeExport(&buf, exportCols, exportRows, ExportJSON); err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	var got jsonResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if len(got.Columns) != 2 || got.Columns[0] != "id" || got.Columns[1] != "name" {
		t.Errorf("columns = %v", got.Columns)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("rows len = %d, want 3", len(got.Rows))
	}
	if got.Rows[0]["name"] != "alice" {
		t.Errorf("row0 name = %q", got.Rows[0]["name"])
	}
	if got.Rows[2]["name"] != "line1\nline2" {
		t.Errorf("row2 name = %q (newline should round-trip)", got.Rows[2]["name"])
	}
}

func TestWriteMarkdown(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeExport(&buf, exportCols, exportRows, ExportMarkdown); err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	out := buf.String()
	// Header + separator + 3 data rows = 5 lines.
	if lines := strings.Count(out, "\n"); lines != 5 {
		t.Errorf("line count = %d, want 5\n%s", lines, out)
	}
	if !strings.Contains(out, "| id | name |") {
		t.Errorf("missing header row: %s", out)
	}
	if !strings.Contains(out, "| --- | --- |") {
		t.Errorf("missing separator row: %s", out)
	}
	// Pipe must be escaped in "bo|b".
	if !strings.Contains(out, `bo\|b`) {
		t.Errorf("pipe not escaped: %s", out)
	}
	// Newline must become <br>.
	if !strings.Contains(out, "line1<br>line2") {
		t.Errorf("newline not escaped: %s", out)
	}
}

func TestWriteMarkdownEmptyRows(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeExport(&buf, exportCols, nil, ExportMarkdown); err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| id | name |") || !strings.Contains(out, "| --- | --- |") {
		t.Errorf("header-only output missing expected structure:\n%s", out)
	}
}
