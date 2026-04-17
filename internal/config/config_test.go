package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataDirCreatesPlatformPath(t *testing.T) {
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("LocalAppData", base)
	default:
		t.Setenv("XDG_DATA_HOME", base)
	}

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}

	want := filepath.Join(base, appName)
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
}

func TestDataDirUnixPermissionsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only permission check")
	}
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %#o, want 0700", got)
	}
}
