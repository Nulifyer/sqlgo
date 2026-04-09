package sqlite

import (
	"context"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/db"
)

func TestBuildDSN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  db.Config
		want string
	}{
		{
			name: "empty path -> in-memory",
			cfg:  db.Config{},
			want: ":memory:",
		},
		{
			name: "explicit :memory: passes through",
			cfg:  db.Config{Database: ":memory:"},
			want: ":memory:",
		},
		{
			name: "plain path with no options",
			cfg:  db.Config{Database: "/tmp/foo.db"},
			want: "/tmp/foo.db",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := buildDSN(tc.cfg); got != tc.want {
				t.Errorf("buildDSN = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOpenInMemoryRoundTrip covers the full Driver.Open -> Conn.Exec ->
// Conn.Query -> Rows lifecycle against an in-memory SQLite database. This
// is the smoke test that proves modernc.org/sqlite is reachable through
// the shared sqlConn wrapper, that streaming Rows works, and that Close()
// is idempotent.
func TestOpenInMemoryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	if got := d.Capabilities(); got.SchemaDepth != db.SchemaDepthFlat || got.LimitSyntax != db.LimitSyntaxLimit {
		t.Fatalf("unexpected capabilities: %+v", got)
	}

	conn, err := d.Open(ctx, db.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	if err := conn.Exec(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, n := range []string{"alice", "bob", "charlotte"} {
		if err := conn.Exec(ctx, `INSERT INTO t(name) VALUES (?)`, n); err != nil {
			t.Fatalf("insert %q: %v", n, err)
		}
	}

	rows, err := conn.Query(ctx, `SELECT id, name FROM t ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	cols := rows.Columns()
	if len(cols) != 2 || cols[0].Name != "id" || cols[1].Name != "name" {
		t.Fatalf("columns = %+v", cols)
	}

	var names []string
	for rows.Next() {
		row, err := rows.Scan()
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(row) != 2 {
			t.Fatalf("row len = %d, want 2", len(row))
		}
		names = append(names, row[1].(string))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	want := []string{"alice", "bob", "charlotte"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}

	// Close() is idempotent.
	if err := rows.Close(); err != nil {
		t.Errorf("rows.Close (second call): %v", err)
	}
}

// TestSchemaFiltersInternalObjects asserts that the schema query hides
// sqlite-internal tables (e.g. sqlite_sequence from AUTOINCREMENT) and
// that everything lands under the synthetic "main" schema so the
// explorer's flat-depth rendering has a single consistent parent.
func TestSchemaFiltersInternalObjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := db.Get(driverName)
	if err != nil {
		t.Fatalf("db.Get: %v", err)
	}
	conn, err := d.Open(ctx, db.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	// AUTOINCREMENT creates sqlite_sequence, which must be filtered out.
	if err := conn.Exec(ctx, `CREATE TABLE thing (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT)`); err != nil {
		t.Fatalf("create thing: %v", err)
	}
	if err := conn.Exec(ctx, `CREATE VIEW v_thing AS SELECT id FROM thing`); err != nil {
		t.Fatalf("create view: %v", err)
	}

	info, err := conn.Schema(ctx)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if len(info.Tables) != 2 {
		t.Fatalf("schema entries = %d, want 2 (got %+v)", len(info.Tables), info.Tables)
	}
	seen := map[string]db.TableKind{}
	for _, tr := range info.Tables {
		if tr.Schema != syntheticSchema {
			t.Errorf("schema = %q, want %q", tr.Schema, syntheticSchema)
		}
		seen[tr.Name] = tr.Kind
	}
	if k, ok := seen["thing"]; !ok || k != db.TableKindTable {
		t.Errorf("expected table 'thing', got %v", seen)
	}
	if k, ok := seen["v_thing"]; !ok || k != db.TableKindView {
		t.Errorf("expected view 'v_thing', got %v", seen)
	}
}
