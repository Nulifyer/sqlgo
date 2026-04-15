package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Nulifyer/sqlgo/cmd/sqlgo/cli"
	"github.com/Nulifyer/sqlgo/internal/tui"
)

func main() {
	// Verb dispatch happens before flag.Parse so the verb's own flags
	// don't collide with the TUI's. Bare "sqlgo" and "sqlgo file.sql"
	// keep their existing meaning.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			os.Exit(int(cli.Dispatch([]string{"version"}, os.Stdin, os.Stdout, os.Stderr)))
		}
		if cli.IsVerb(os.Args[1]) {
			os.Exit(int(cli.Dispatch(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)))
		}
	}

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "usage: %s\n", os.Args[0])
		fmt.Fprintf(out, "       %s <verb> [flags]    verbs: exec, export, open, edit, conns, history, version\n", os.Args[0])
		fmt.Fprintf(out, "       %s --version\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	// Bare positional args no longer map to "open FILE.sql in TUI" --
	// that shorthand moved to `sqlgo edit FILE.sql`. Anything left over
	// here is a mistyped verb.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "sqlgo: unknown verb %q -- try 'sqlgo help'\n", flag.Arg(0))
		os.Exit(int(cli.ExitUsage))
	}

	if err := tui.Run(tui.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "sqlgo:", err)
		os.Exit(1)
	}
}
