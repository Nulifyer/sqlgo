package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAt(context.Background(), filepath.Join(t.TempDir(), "sqlgo.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleConn(name string) config.Connection {
	return config.Connection{
		Name:     name,
		Driver:   "mssql",
		Host:     "db.example.com",
		Port:     1433,
		User:     "sa",
		Password: "p@ss",
		Database: "app",
		Options:  map[string]string{"encrypt": "disable", "TrustServerCertificate": "true"},
	}
}

func TestSaveAndListConnections(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"beta", "alpha", "gamma"} {
		if err := s.SaveConnection(ctx, "", sampleConn(name)); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}

	list, err := s.ListConnections(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Sorted by name.
	want := []string{"alpha", "beta", "gamma"}
	if len(list) != len(want) {
		t.Fatalf("list len = %d, want %d", len(list), len(want))
	}
	for i, c := range list {
		if c.Name != want[i] {
			t.Errorf("list[%d].Name = %q, want %q", i, c.Name, want[i])
		}
		if c.Options["encrypt"] != "disable" {
			t.Errorf("list[%d].Options lost: %+v", i, c.Options)
		}
	}
}

func TestSaveConnectionUpsertOverwrites(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	c := sampleConn("prod")
	if err := s.SaveConnection(ctx, "", c); err != nil {
		t.Fatalf("save: %v", err)
	}
	c.Host = "new-host"
	c.Port = 2222
	if err := s.SaveConnection(ctx, "", c); err != nil {
		t.Fatalf("save update: %v", err)
	}

	got, err := s.GetConnection(ctx, "prod")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Host != "new-host" || got.Port != 2222 {
		t.Errorf("update not applied: %+v", got)
	}

	list, err := s.ListConnections(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1 after upsert", len(list))
	}
}

func TestSaveConnectionRename(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveConnection(ctx, "", sampleConn("old")); err != nil {
		t.Fatalf("save: %v", err)
	}
	renamed := sampleConn("new")
	if err := s.SaveConnection(ctx, "old", renamed); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if _, err := s.GetConnection(ctx, "old"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("old still present: err=%v", err)
	}
	if _, err := s.GetConnection(ctx, "new"); err != nil {
		t.Errorf("new missing: %v", err)
	}
}

func TestDeleteConnectionNotFound(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.DeleteConnection(ctx, "missing"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("delete missing: got %v, want ErrConnectionNotFound", err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	src := openTestStore(t)
	dst := openTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		if err := src.SaveConnection(ctx, "", sampleConn(name)); err != nil {
			t.Fatalf("save src %s: %v", name, err)
		}
	}

	var buf bytes.Buffer
	if err := src.ExportJSON(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Sanity check that the exported JSON looks like the legacy shape:
	// it must have a "connections" top-level array. This keeps the
	// format hand-editable.
	if !strings.Contains(buf.String(), `"connections"`) {
		t.Errorf("exported JSON missing connections key: %s", buf.String())
	}

	n, err := dst.ImportJSON(ctx, &buf)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 2 {
		t.Errorf("imported count = %d, want 2", n)
	}

	list, err := dst.ListConnections(ctx)
	if err != nil {
		t.Fatalf("list dst: %v", err)
	}
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Errorf("dst list = %+v", list)
	}
}

func TestOptionsNilAndEmptyRoundTrip(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	// Nil options should come back nil, not an empty-but-non-nil map.
	c := sampleConn("noopts")
	c.Options = nil
	if err := s.SaveConnection(ctx, "", c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.GetConnection(ctx, "noopts")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Options != nil {
		t.Errorf("expected nil Options, got %+v", got.Options)
	}
}
