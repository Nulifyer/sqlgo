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
	if len(os.Args) > 1 && cli.IsVerb(os.Args[1]) {
		os.Exit(int(cli.Dispatch(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)))
	}

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "usage: %s [file.sql]\n", os.Args[0])
		fmt.Fprintf(out, "       %s <verb> [flags]    verbs: exec, export, conns, history\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	opts := tui.Options{}
	if flag.NArg() > 0 {
		path := flag.Arg(0)
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sqlgo:", err)
			os.Exit(1)
		}
		opts.InitialQuery = string(data)
	}

	if err := tui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "sqlgo:", err)
		os.Exit(1)
	}
}
