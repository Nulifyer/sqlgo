package db

import (
	"path/filepath"
	"testing"
)

func TestProfileStoreSaveAsRenamesProfile(t *testing.T) {
	store := &ProfileStore{path: filepath.Join(t.TempDir(), "profiles.json")}

	original := ConnectionProfile{Name: "old-name", ProviderID: ProviderSQLite, Settings: ConnectionSettings{FilePath: "test.db"}}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	renamed := original
	renamed.Name = "new-name"
	if err := store.SaveAs(renamed, "old-name"); err != nil {
		t.Fatalf("SaveAs() error = %v", err)
	}

	profiles, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len(profiles) = %d, want 1", len(profiles))
	}
	if profiles[0].Name != "new-name" {
		t.Fatalf("profiles[0].Name = %q, want new-name", profiles[0].Name)
	}
}
