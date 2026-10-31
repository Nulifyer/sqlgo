package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedCommitPreservesDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.sql")
	os.WriteFile(path, []byte("original"), 0600)
	out, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	out.Writer().Write([]byte("replacement"))
	out.file.Close()
	if err := out.Finish(true); err == nil {
		t.Fatal("closed temporary file should fail to commit")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Fatalf("failed commit changed destination to %q", data)
	}
	if _, err := os.Stat(out.temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary file was not removed: %v", err)
	}
}
func TestWriteFileReplacesCompleteContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.sql")
	os.WriteFile(path, []byte("original"), 0600)
	if err := WriteFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("contents=%q", data)
	}
}
