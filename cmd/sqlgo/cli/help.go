package cli

import (
	"io"
)

// runHelp prints either the master usage or, when given a verb name,
// re-enters Dispatch with `-h` so the verb's own usage is the single
// source of truth. `sqlgo help unknown-verb` falls back to the master
// usage on stderr with a non-zero exit so a typo in a script is loud.
func runHelp(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		masterHelp(stdout)
		return ExitOK
	}
	verb := argv[0]
	if !IsVerb(verb) || verb == "help" || verb == "-h" || verb == "--help" {
		masterHelp(stdout)
		return ExitOK
	}
	return Dispatch([]string{verb, "-h"}, stdin, stdout, stderr)
}

func masterHelp(w io.Writer) {
	io.WriteString(w, `sqlgo -- terminal SQL workbench and scripting CLI

usage:
  sqlgo                          launch the TUI
  sqlgo <verb> [flags]           run a non-interactive command
  sqlgo help <verb>              show help for a verb
  sqlgo --version                print version

verbs:
  exec      run SQL and print results (table on tty, tsv on a pipe)
  export    run SQL and write results to a file (default csv)
  open      query CSV/TSV/JSONL files in-memory (no saved connection)
  edit      launch the TUI with FILE.sql preloaded in the editor
  conns     manage saved connections (list, show, add, set, rm, test, import, export)
  history   inspect query history (list, search, clear)
  version     print sqlgo version
  help        show this message or per-verb help
  completion  print shell-completion script for bash|zsh|fish|powershell|pwsh

exit codes:
  0  ok
  1  usage / argument error
  2  connection / store error
  3  query error
  4  refused: unsafe mutation without --allow-unsafe
  5  --max-rows cap reached (partial output flushed)

see https://github.com/Nulifyer/sqlgo for full docs.
`)
}
