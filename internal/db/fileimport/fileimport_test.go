package fileimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCSVInfersTypes(t *testing.T) {
	t.Parallel()
	p := writeFile(t, "sales.csv", "id,price,label\n1,9.50,apple\n2,10,pear\n3,,\n")
	db := openMem(t)
	table, err := Load(context.Background(), db, p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if table != "sales" {
		t.Errorf("table = %q, want sales", table)
	}
	var colType string
	row := db.QueryRow(`SELECT type FROM pragma_table_info('sales') WHERE name = 'id'`)
	if err := row.Scan(&colType); err != nil {
		t.Fatalf("id type: %v", err)
	}
	if colType != "INTEGER" {
		t.Errorf("id type = %q, want INTEGER", colType)
	}
	row = db.QueryRow(`SELECT type FROM pragma_table_info('sales') WHERE name = 'price'`)
	if err := row.Scan(&colType); err != nil {
		t.Fatalf("price type: %v", err)
	}
	if colType != "REAL" {
		t.Errorf("price type = %q, want REAL", colType)
	}
	row = db.QueryRow(`SELECT type FROM pragma_table_info('sales') WHERE name = 'label'`)
	if err := row.Scan(&colType); err != nil {
		t.Fatalf("label type: %v", err)
	}
	if colType != "TEXT" {
		t.Errorf("label type = %q, want TEXT", colType)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sales`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("row count = %d, want 3", n)
	}
}

func TestLoadTSV(t *testing.T) {
	t.Parallel()
	p := writeFile(t, "data.tsv", "a\tb\n1\thello\n2\tworld\n")
	db := openMem(t)
	if _, err := Load(context.Background(), db, p); err != nil {
		t.Fatalf("load: %v", err)
	}
	var s string
	if err := db.QueryRow(`SELECT b FROM data WHERE a = 2`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != "world" {
		t.Errorf("got %q, want world", s)
	}
}

func TestLoadJSONL(t *testing.T) {
	t.Parallel()
	body := `{"id":1,"name":"a"}
{"id":2,"name":"b","extra":"x"}
{"id":3,"name":"c"}
`
	p := writeFile(t, "people.jsonl", body)
	db := openMem(t)
	if _, err := Load(context.Background(), db, p); err != nil {
		t.Fatalf("load: %v", err)
	}
	var cols []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('people') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, n)
	}
	rows.Close()
	want := []string{"extra", "id", "name"}
	if len(cols) != len(want) {
		t.Fatalf("cols = %v, want %v", cols, want)
	}
	for i := range cols {
		if cols[i] != want[i] {
			t.Errorf("cols[%d] = %q, want %q", i, cols[i], want[i])
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM people WHERE extra IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("null-extra rows = %d, want 2", n)
	}
}

func TestTableNameSanitization(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"sales.csv":      "sales",
		"sales 2024.csv": "sales_2024",
		"01-raw.csv":     "t_01_raw",
		"weird!@#.jsonl": "weird___",
		"/path/to/x.tsv": "x",
		"no-ext":         "no_ext",
	}
	for in, want := range cases {
		if got := TableName(in); got != want {
			t.Errorf("TableName(%q) = %q, want %q", in, got, want)
		}
	}
}
