package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/atomicfile"
)

func TestAtomicOutputAbortPreservesDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := atomicfile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Writer().Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if err := out.Finish(false); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("destination = %q, want original contents", got)
	}
}

func TestRunAbortPreservesDestinationBeforeSafetyApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := atomicfile.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := run(t.Context(), &runOptions{
		sql:       "DROP TABLE users",
		out:       out.Writer(),
		outFinish: out.Finish,
		stderr:    &stderr,
	})
	if code != ExitUnsafeRefused {
		t.Fatalf("code = %d, want %d; stderr = %q", code, ExitUnsafeRefused, stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("destination = %q, want original contents", got)
	}
}

func TestAtomicOutputCommitReplacesDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	out, err := atomicfile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Writer().Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if err := out.Finish(true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Fatalf("destination = %q, want replacement contents", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Fatalf("destination mode = %o, want %o", got, want)
	}
}
