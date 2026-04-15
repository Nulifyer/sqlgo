package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/Nulifyer/sqlgo/internal/output"
)

// runExec is the handler for `sqlgo exec`. It runs one or more SQL
// statements and writes any result rows to stdout (or -o). Default
// format is table when stdout is a tty, tsv otherwise, matching what
// a shell user would expect from a `psql`-style tool.
func runExec(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags := &commonFlags{}
	flags.registerCommon(fs)
	fs.Usage = func() {
		io.WriteString(stderr, "usage: sqlgo exec (-c NAME | --dsn URL) [-q SQL | -f FILE | stdin] [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}

	defFmt := output.TSV
	if isTerminal(stdout) && flags.Output == "" {
		defFmt = output.Table
	}
	opts, code, err := buildRunOptions(flags, stdin, stdout, stderr, defFmt)
	if err != nil {
		stderrf(stderr, "%v", err)
		return code
	}

	return run(context.Background(), opts)
}
