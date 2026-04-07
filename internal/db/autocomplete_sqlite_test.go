package db

import (
	"context"
	"testing"
)

func TestLoadCompletionMetadataSQLite(t *testing.T) {
	t.Parallel()

	profile := testSQLiteProfile(t)
	meta, err := LoadCompletionMetadata(context.Background(), profile, DefaultRegistry())
	if err != nil {
		t.Fatalf("LoadCompletionMetadata() error = %v", err)
	}
	if len(meta.Catalogs) != 1 || meta.Catalogs[0] != profile.Name {
		t.Fatalf("unexpected catalogs: %#v", meta.Catalogs)
	}
	if len(meta.Objects) == 0 {
		t.Fatalf("expected objects in completion metadata")
	}

	foundUsers := false
	for _, object := range meta.Objects {
		if object.Name != "users" {
			continue
		}
		foundUsers = true
		if len(object.Columns) == 0 {
			t.Fatalf("users object should have columns")
		}
	}
	if !foundUsers {
		t.Fatalf("users table not found in completion metadata")
	}
}
