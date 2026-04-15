package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

var testCols = []db.Column{{Name: "id"}, {Name: "name"}}
var testRows = [][]string{
	{"1", "alice"},
	{"2", "bo|b"},
	{"3", "line1\nline2"},
}

func TestFormatFromPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		want  Format
		known bool
	}{
		{"out.csv", CSV, true},
		{"out.tsv", TSV, true},
		{"out.json", JSON, true},
		{"out.md", Markdown, true},
		{"out.markdown", Markdown, true},
		{"out.txt", CSV, false},
		{"out", CSV, false},
	}
	for _, tc := range cases {
		got, known := FormatFromPath(tc.path)
		if got != tc.want || known != tc.known {
			t.Errorf("FormatFromPath(%q) = (%v,%v), want (%v,%v)", tc.path, got, known, tc.want, tc.known)
		}
	}
}

func TestFormatFromName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want Format
		ok   bool
	}{
		{"csv", CSV, true},
		{"TSV", TSV, true},
		{" json ", JSON, true},
		{"md", Markdown, true},
		{"markdown", Markdown, true},
		{"yaml", 0, false},
	}
	for _, tc := range cases {
		got, err := FormatFromName(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("FormatFromName(%q) err=%v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("FormatFromName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestWriteCSV(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Write(&buf, testCols, testRows, CSV); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "id,name\n1,alice\n2,bo|b\n3,\"line1\nline2\"\n"
	if buf.String() != want {
		t.Errorf("csv =\n%q\nwant\n%q", buf.String(), want)
	}
}

func TestWriteTSV(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Write(&buf, testCols, testRows, TSV); err != nil {
		t.Fatalf("Write: %v", err)
	}
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
	if err := Write(&buf, testCols, testRows, JSON); err != nil {
		t.Fatalf("Write: %v", err)
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
	if err := Write(&buf, testCols, testRows, Markdown); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	if lines := strings.Count(out, "\n"); lines != 5 {
		t.Errorf("line count = %d, want 5\n%s", lines, out)
	}
	if !strings.Contains(out, "| id | name |") {
		t.Errorf("missing header row: %s", out)
	}
	if !strings.Contains(out, "| --- | --- |") {
		t.Errorf("missing separator row: %s", out)
	}
	if !strings.Contains(out, `bo\|b`) {
		t.Errorf("pipe not escaped: %s", out)
	}
	if !strings.Contains(out, "line1<br>line2") {
		t.Errorf("newline not escaped: %s", out)
	}
}

func TestWriteMarkdownEmptyRows(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Write(&buf, testCols, nil, Markdown); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| id | name |") || !strings.Contains(out, "| --- | --- |") {
		t.Errorf("header-only output missing expected structure:\n%s", out)
	}
}
