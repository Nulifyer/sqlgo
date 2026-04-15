package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/Nulifyer/sqlgo/internal/output"
)

// runExport handles `sqlgo export`. It is `exec` with different
// defaults: an output path is required (-o) and the format is inferred
// from its extension unless --format overrides. Intended for
// unattended runs that turn a query into a file.
func runExport(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags := &commonFlags{}
	flags.registerCommon(fs)
	fs.Usage = func() {
		io.WriteString(stderr, "usage: sqlgo export (-c NAME | --dsn URL) -o FILE [-q SQL | -f FILE | stdin] [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}

	// export always writes to stdout when -o is absent, using CSV as a
	// safe default. The more opinionated "you must give -o" policy we
	// discussed is softened here: pipelines like `sqlgo export -c foo
	// -q ... | ...` should still work, and CSV is the industry-default
	// "tabular dump" shape.
	defFmt := output.CSV
	opts, code, err := buildRunOptions(flags, stdin, stdout, stderr, defFmt)
	if err != nil {
		stderrf(stderr, "%v", err)
		return code
	}

	return run(context.Background(), opts)
}
