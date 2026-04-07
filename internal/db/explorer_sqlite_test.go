package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLoadExplorerSnapshotSQLite(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "explorer.db")
	if err := CreateSQLiteFixture(context.Background(), dbPath); err != nil {
		t.Fatalf("CreateSQLiteFixture() error = %v", err)
	}

	profile := ConnectionProfile{
		Name:       "fixture",
		ProviderID: ProviderSQLite,
		DSN:        "file:" + filepath.ToSlash(dbPath),
	}

	snapshot, err := LoadExplorerSnapshot(context.Background(), profile, DefaultRegistry())
	if err != nil {
		t.Fatalf("LoadExplorerSnapshot() error = %v", err)
	}

	if len(snapshot.Databases) != 1 {
		t.Fatalf("len(Databases) = %d, want 1", len(snapshot.Databases))
	}
	if len(snapshot.Tables) != 3 {
		t.Fatalf("len(Tables) = %d, want 3", len(snapshot.Tables))
	}
	if len(snapshot.Views) != 0 {
		t.Fatalf("len(Views) = %d, want 0", len(snapshot.Views))
	}

	wantTables := map[string]bool{
		"events":   false,
		"projects": false,
		"users":    false,
	}
	for _, table := range snapshot.Tables {
		if _, ok := wantTables[table.Name]; ok {
			wantTables[table.Name] = true
		}
	}
	for name, found := range wantTables {
		if !found {
			t.Fatalf("table %q not found in snapshot", name)
		}
	}
}
