package db

import (
	"path/filepath"
	"testing"
	"time"
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

func TestProfileStoreSaveAsPreservesCreatedAtOnUpdate(t *testing.T) {
	store := &ProfileStore{path: filepath.Join(t.TempDir(), "profiles.json")}

	createdAt := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	original := ConnectionProfile{
		Name:       "dev",
		ProviderID: ProviderSQLite,
		Settings:   ConnectionSettings{FilePath: "test.db"},
		CreatedAt:  createdAt,
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	updated := ConnectionProfile{
		Name:       "dev",
		ProviderID: ProviderSQLite,
		Settings:   ConnectionSettings{FilePath: "updated.db"},
	}
	if err := store.Save(updated); err != nil {
		t.Fatalf("Save(updated) error = %v", err)
	}

	profiles, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len(profiles) = %d, want 1", len(profiles))
	}
	if got := profiles[0].CreatedAt; !got.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want %v", got, createdAt)
	}
}
