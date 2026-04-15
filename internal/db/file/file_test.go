package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestOpenLoadsCSV(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.csv")
	b := filepath.Join(dir, "b.tsv")
	if err := os.WriteFile(a, []byte("id,v\n1,hi\n2,bye\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("k\tv\nx\t10\ny\t20\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := db.Get("file")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := d.Open(context.Background(), db.Config{Database: a + ";" + b})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	info, err := conn.Schema(context.Background())
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	names := map[string]bool{}
	for _, tr := range info.Tables {
		names[tr.Name] = true
	}
	if !names["a"] || !names["b"] {
		t.Errorf("tables = %v, want a and b", info.Tables)
	}

	rows, err := conn.Query(context.Background(), `SELECT v FROM a WHERE id = 2`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no row")
	}
	r, err := rows.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r[0].(string); got != "bye" {
		t.Errorf("got %v, want bye", r[0])
	}
}

func TestSplitPaths(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"":             nil,
		"a.csv":        {"a.csv"},
		"a.csv;b.csv":  {"a.csv", "b.csv"},
		"a.csv, b.csv": {"a.csv", "b.csv"},
		"  a.csv  ; ":  {"a.csv"},
	}
	for in, want := range cases {
		got := splitPaths(in)
		if len(got) != len(want) {
			t.Errorf("splitPaths(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("splitPaths(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}
