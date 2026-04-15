package cli

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nulifyer/sqlgo/internal/tui"
)

// editAllowedExts is the set of extensions `sqlgo edit` will load.
// Kept intentionally narrow: the TUI editor is for SQL, not a general
// text editor, so opening a .png or .exe is almost always a typo.
var editAllowedExts = map[string]struct{}{
	".sql": {},
	".txt": {},
}

// runEdit handles `sqlgo edit FILE.sql`. It launches the TUI with the
// file preloaded into the query editor. Replaces the older bare
// `sqlgo FILE.sql` shorthand so the verb surface is uniform.
func runEdit(argv []string, _ io.Reader, _, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		io.WriteString(stderr, "usage: sqlgo edit FILE.sql\n")
		io.WriteString(stderr, "\nLaunches the TUI with FILE preloaded into the query editor.\n")
		io.WriteString(stderr, "Allowed extensions: .sql, .txt\n")
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		stderrf(stderr, "edit: expected exactly one file")
		fs.Usage()
		return ExitUsage
	}
	path := fs.Arg(0)
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := editAllowedExts[ext]; !ok {
		stderrf(stderr, "edit: unsupported extension %q (expected .sql or .txt)", ext)
		return ExitUsage
	}
	if _, err := os.Stat(path); err != nil {
		stderrf(stderr, "edit: %v", err)
		return ExitUsage
	}
	data, err := os.ReadFile(path)
	if err != nil {
		stderrf(stderr, "edit: %v", err)
		return 1
	}
	if err := tui.Run(tui.Options{InitialQuery: string(data)}); err != nil {
		stderrf(stderr, "%v", err)
		return 1
	}
	return ExitOK
}
