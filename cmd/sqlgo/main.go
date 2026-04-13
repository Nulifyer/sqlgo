package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Nulifyer/sqlgo/internal/tui"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [file.sql]\n", os.Args[0])
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
