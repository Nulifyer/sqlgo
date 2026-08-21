package widget

import (
	"path/filepath"
	"testing"
)

func TestFilePickerPathDoesNotDuplicateSelectedExtension(t *testing.T) {
	fp := NewFilePicker(FilePickerOpts{
		Mode:    ModeSaveTarget,
		Dir:     t.TempDir(),
		Name:    "report.SQL",
		Choices: []ExtChoice{{Ext: ".sql"}},
	})

	if got, want := filepath.Base(fp.Path()), "report.SQL"; got != want {
		t.Fatalf("Path base = %q, want %q", got, want)
	}
}
