package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Shell completion support. The `completion <shell>` verb prints a
// ready-to-source script for the requested shell. A hidden `__complete`
// verb is what those scripts call for anything dynamic (currently just
// saved connection names); everything else is baked into the script so
// users do not need to re-source after adding new verbs unless they
// update sqlgo itself.

var completionShells = []string{"bash", "zsh", "fish", "powershell", "pwsh"}

// topVerbs are the user-visible verbs exposed to completion. The hidden
// __complete helper is intentionally omitted so it does not surface in
// tab-completion menus.
var topVerbs = []string{
	"exec", "export", "open", "edit", "conns", "history",
	"version", "help", "completion",
}

// verbFlags lists every long/short flag each verb accepts. Keep aligned
// with the actual fs.*Var calls in the per-verb files -- shell scripts
// are dumb and will not discover new flags on their own.
var verbFlags = map[string][]string{
	"exec": {
		"-c", "--conn", "--dsn", "--password-stdin",
		"-q", "--query", "-f", "--file",
		"--format", "-o", "--output",
		"--allow-unsafe", "--continue-on-error", "--record-history",
		"--timeout", "--max-rows",
	},
	"export": {
		"-c", "--conn", "--dsn", "--password-stdin",
		"-q", "--query", "-f", "--file",
		"--format", "-o", "--output",
		"--max-rows", "--timeout",
	},
	"open": {
		"-c", "--conn", "--dsn", "--password-stdin",
	},
	"edit": {
		"-c", "--conn", "--dsn", "--password-stdin",
	},
	"history": {
		"--limit", "-c", "--conn",
	},
	"conns":      {},
	"version":    {},
	"help":       {},
	"completion": {},
}

// verbSubs lists subverbs for verbs that have them. Shell scripts offer
// these as the first completion after the parent verb.
var verbSubs = map[string][]string{
	"conns":   {"list", "show", "add", "set", "rm", "test", "import", "export", "help"},
	"history": {"list", "search", "clear", "help"},
}

// connsSubFlags covers the flags each `sqlgo conns <sub>` accepts that
// are worth offering in completion. Kept short; the shells still let
// the user type unlisted flags verbatim.
var connsSubFlags = map[string][]string{
	"list":   {"--format"},
	"show":   {},
	"add":    {"--driver", "--host", "--port", "--user", "--database", "--password-stdin", "--keyring", "--ssh-host", "--ssh-port", "--ssh-user", "--ssh-key", "--ssh-password-stdin", "--force"},
	"set":    {"--driver", "--host", "--port", "--user", "--database", "--password-stdin", "--keyring", "--ssh-host", "--ssh-port", "--ssh-user", "--ssh-key", "--ssh-password-stdin", "--force"},
	"rm":     {"--force"},
	"test":   {"--password-stdin", "--ssh-password-stdin", "--timeout"},
	"import": {"-i", "--input"},
	"export": {"-o", "--output"},
}

var historySubFlags = map[string][]string{
	"list":   {"--limit", "-c", "--conn"},
	"search": {"--limit", "-c", "--conn"},
	"clear":  {"-c", "--conn", "--force"},
}

// flagValues maps flags that take enumerated values to the candidate
// list. Unlisted flags default to "no value suggestions".
var flagValues = map[string][]string{
	"--format": {"csv", "tsv", "json", "jsonl", "markdown", "table"},
	"--driver": {"mssql", "sqlserver", "postgres", "postgresql", "mysql", "sqlite", "duckdb", "odbc"},
}

// runCompletion prints the completion script for the requested shell.
func runCompletion(argv []string, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "usage: sqlgo completion <bash|zsh|fish|powershell|pwsh>")
		return ExitUsage
	}
	switch strings.ToLower(argv[0]) {
	case "bash":
		io.WriteString(stdout, bashCompletionScript())
	case "zsh":
		io.WriteString(stdout, zshCompletionScript())
	case "fish":
		io.WriteString(stdout, fishCompletionScript())
	case "powershell", "pwsh":
		io.WriteString(stdout, pwshCompletionScript())
	default:
		fmt.Fprintf(stderr, "sqlgo completion: unknown shell %q (want: %s)\n",
			argv[0], strings.Join(completionShells, "|"))
		return ExitUsage
	}
	return ExitOK
}

// runHiddenComplete is the data side of completion. Shell scripts call
// `sqlgo __complete <kind> [arg]` and parse one entry per line. Kept
// out of IsVerb so it does not show up in help or in tab-completion
// menus, but still routed through Dispatch so tests can drive it.
func runHiddenComplete(argv []string, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		return ExitUsage
	}
	switch argv[0] {
	case "conns":
		return completeConnNames(stdout, stderr)
	case "verbs":
		for _, v := range topVerbs {
			fmt.Fprintln(stdout, v)
		}
		return ExitOK
	case "shells":
		for _, s := range completionShells {
			fmt.Fprintln(stdout, s)
		}
		return ExitOK
	case "formats":
		for _, f := range flagValues["--format"] {
			fmt.Fprintln(stdout, f)
		}
		return ExitOK
	case "drivers":
		for _, d := range flagValues["--driver"] {
			fmt.Fprintln(stdout, d)
		}
		return ExitOK
	}
	return ExitUsage
}

func completeConnNames(stdout, stderr io.Writer) ExitCode {
	st, err := openStore(context.Background())
	if err != nil {
		return ExitConn
	}
	defer st.Close()
	conns, err := st.ListConnections(context.Background())
	if err != nil {
		return ExitConn
	}
	names := make([]string, 0, len(conns))
	for _, c := range conns {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintln(stdout, n)
	}
	return ExitOK
}

// --- script templates ------------------------------------------------------

func bashCompletionScript() string {
	verbs := strings.Join(topVerbs, " ")
	var b strings.Builder
	b.WriteString(`# sqlgo bash completion. Source this file or save it to
# /etc/bash_completion.d/sqlgo (or ~/.local/share/bash-completion/completions/sqlgo).

_sqlgo() {
    local cur prev words cword
    _init_completion || return

    local verb=""
    local i
    for (( i=1; i<cword; i++ )); do
        case "${words[i]}" in
            -*) continue ;;
            *)  verb="${words[i]}"; break ;;
        esac
    done

    # Value completion for the flag just before the cursor.
    case "$prev" in
        --format)    COMPREPLY=( $(compgen -W "csv tsv json jsonl markdown table" -- "$cur") ); return ;;
        --driver)    COMPREPLY=( $(compgen -W "mssql sqlserver postgres postgresql mysql sqlite duckdb odbc" -- "$cur") ); return ;;
        -c|--conn)   COMPREPLY=( $(compgen -W "$(sqlgo __complete conns 2>/dev/null)" -- "$cur") ); return ;;
        -f|--file|-i|--input|-o|--output|--ssh-key)
                     _filedir; return ;;
    esac

    if [[ -z "$verb" ]]; then
`)
	fmt.Fprintf(&b, "        COMPREPLY=( $(compgen -W %q -- \"$cur\") )\n", verbs)
	b.WriteString(`        return
    fi

    local flags=""
    local subs=""
    case "$verb" in
`)
	for _, v := range topVerbs {
		fmt.Fprintf(&b, "        %s)\n", v)
		if subs, ok := verbSubs[v]; ok {
			fmt.Fprintf(&b, "            subs=%q\n", strings.Join(subs, " "))
		}
		if flags, ok := verbFlags[v]; ok && len(flags) > 0 {
			fmt.Fprintf(&b, "            flags=%q\n", strings.Join(flags, " "))
		}
		b.WriteString("            ;;\n")
	}
	b.WriteString(`    esac

    # If the verb has subverbs and no subverb has been typed yet, offer them.
    if [[ -n "$subs" ]]; then
        local j sub_seen=""
        for (( j=1; j<cword; j++ )); do
            case "${words[j]}" in
                -*) continue ;;
                "$verb") continue ;;
                *) sub_seen="${words[j]}"; break ;;
            esac
        done
        if [[ -z "$sub_seen" ]]; then
            COMPREPLY=( $(compgen -W "$subs" -- "$cur") )
            return
        fi
    fi

    if [[ "$cur" == -* || -n "$flags" ]]; then
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
    fi
}

complete -F _sqlgo sqlgo
`)
	return b.String()
}

func zshCompletionScript() string {
	verbs := strings.Join(topVerbs, " ")
	var b strings.Builder
	b.WriteString(`#compdef sqlgo
# sqlgo zsh completion. Save as _sqlgo somewhere on $fpath (e.g.
# ~/.zfunc/_sqlgo) and make sure ` + "`autoload -U compinit && compinit`" + ` runs.

_sqlgo() {
    local -a verbs
    verbs=(` + verbs + `)

    local -a formats drivers shells
    formats=(csv tsv json jsonl markdown table)
    drivers=(mssql sqlserver postgres postgresql mysql sqlite duckdb odbc)
    shells=(bash zsh fish powershell pwsh)

    local state verb line
    _arguments -C \
        '1: :->verb' \
        '*::arg:->args'

    case $state in
        verb)
            _describe 'sqlgo verb' verbs
            ;;
        args)
            verb=${words[1]}
            case $verb in
`)
	for _, v := range topVerbs {
		fmt.Fprintf(&b, "                %s) _sqlgo_%s ;;\n", v, v)
	}
	b.WriteString(`            esac
            ;;
    esac
}

_sqlgo_common_flags() {
    _arguments \
        '(-c --conn)'{-c,--conn}'[saved connection]:connection:_sqlgo_conn_names' \
        '--dsn[inline DSN]:dsn:' \
        '--password-stdin[read password from stdin]' \
        '--format[output format]:format:(csv tsv json jsonl markdown table)' \
        '(-o --output)'{-o,--output}'[output file]:file:_files' \
        '(-f --file)'{-f,--file}'[sql file]:file:_files -g "*.sql"' \
        '(-q --query)'{-q,--query}'[sql text]:sql:' \
        '--timeout[per-statement timeout]:duration:' \
        '--max-rows[row cap]:n:'
}

_sqlgo_conn_names() {
    local -a names
    names=( ${(f)"$(sqlgo __complete conns 2>/dev/null)"} )
    _describe 'connection' names
}

_sqlgo_exec()       { _sqlgo_common_flags; _arguments '--allow-unsafe[permit destructive statements]' '--continue-on-error[keep running after failure]' '--record-history[append to history]' }
_sqlgo_export()     { _sqlgo_common_flags }
_sqlgo_open()       { _sqlgo_common_flags; _files -g '*.(csv|tsv|jsonl|json)' }
_sqlgo_edit()       { _sqlgo_common_flags; _files -g '*.sql' }
_sqlgo_version()    { }
_sqlgo_help()       { _describe 'verb' verbs }
_sqlgo_completion() { _values 'shell' bash zsh fish powershell pwsh }

_sqlgo_conns() {
    local -a subs
    subs=(list show add set rm test import export help)
    _arguments -C '1: :->sub' '*::arg:->args'
    case $state in
        sub) _describe 'conns subverb' subs ;;
        args)
            case ${words[1]} in
                add|set) _arguments '--driver[driver]:driver:(mssql sqlserver postgres postgresql mysql sqlite duckdb odbc)' '--host[host]:' '--port[port]:' '--user[user]:' '--database[database]:' '--password-stdin' '--force' '--ssh-host:' '--ssh-port:' '--ssh-user:' '--ssh-key:file:_files' ;;
                rm)      _arguments '--force[required]' ':connection:_sqlgo_conn_names' ;;
                test|show) _sqlgo_conn_names ;;
                list)    _arguments '--format:format:(csv tsv json jsonl markdown table)' ;;
                import)  _arguments '(-i --input)'{-i,--input}':file:_files' ;;
                export)  _arguments '(-o --output)'{-o,--output}':file:_files' ;;
            esac ;;
    esac
}

_sqlgo_history() {
    local -a subs
    subs=(list search clear help)
    _arguments -C '1: :->sub' '*::arg:->args'
    case $state in
        sub)  _describe 'history subverb' subs ;;
        args) _arguments '--limit:n:' '(-c --conn)'{-c,--conn}':connection:_sqlgo_conn_names' '--force' ;;
    esac
}

_sqlgo "$@"
`)
	return b.String()
}

func fishCompletionScript() string {
	var b strings.Builder
	b.WriteString(`# sqlgo fish completion. Save as ~/.config/fish/completions/sqlgo.fish.

function __sqlgo_verb
    set -l cmd (commandline -opc)
    for i in (seq 2 (count $cmd))
        switch $cmd[$i]
            case '-*'
            case '*'
                echo $cmd[$i]; return
        end
    end
end

function __sqlgo_no_verb
    test -z (__sqlgo_verb)
end

function __sqlgo_verb_is
    test (__sqlgo_verb) = $argv[1]
end

function __sqlgo_conns
    sqlgo __complete conns 2>/dev/null
end

# Top-level verbs.
`)
	for _, v := range topVerbs {
		fmt.Fprintf(&b, "complete -c sqlgo -f -n __sqlgo_no_verb -a %s\n", v)
	}
	b.WriteString(`
# Shared flags on verbs that connect to a database.
for verb in exec export open edit history
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -s c -l conn -x -a '(__sqlgo_conns)' -d 'saved connection'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -l dsn -x -d 'inline DSN'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -l password-stdin -d 'read password from stdin'
end

for verb in exec export
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -s q -l query -x -d 'SQL text'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -s f -l file -r -d 'SQL file'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -l format -x -a 'csv tsv json jsonl markdown table'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -s o -l output -r -d 'output file'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -l timeout -x -d 'per-statement timeout'
    complete -c sqlgo -n "__sqlgo_verb_is $verb" -l max-rows -x -d 'row cap'
end

complete -c sqlgo -n '__sqlgo_verb_is exec' -l allow-unsafe
complete -c sqlgo -n '__sqlgo_verb_is exec' -l continue-on-error
complete -c sqlgo -n '__sqlgo_verb_is exec' -l record-history

complete -c sqlgo -n '__sqlgo_verb_is completion' -x -a 'bash zsh fish powershell pwsh'
complete -c sqlgo -n '__sqlgo_verb_is help' -x -a 'exec export open edit conns history version completion'

# conns subverbs.
complete -c sqlgo -n '__sqlgo_verb_is conns' -x -a 'list show add set rm test import export help'
complete -c sqlgo -n '__sqlgo_verb_is conns' -l driver -x -a 'mssql sqlserver postgres postgresql mysql sqlite duckdb odbc'
complete -c sqlgo -n '__sqlgo_verb_is conns' -l format -x -a 'csv tsv json jsonl markdown table'
complete -c sqlgo -n '__sqlgo_verb_is conns' -l force

# history subverbs.
complete -c sqlgo -n '__sqlgo_verb_is history' -x -a 'list search clear help'
complete -c sqlgo -n '__sqlgo_verb_is history' -l limit -x
`)
	return b.String()
}

func pwshCompletionScript() string {
	verbs := strings.Join(quoteForPwsh(topVerbs), ", ")
	var b strings.Builder
	b.WriteString(`# sqlgo PowerShell completion. Dot-source or put in $PROFILE:
#   sqlgo completion pwsh | Out-String | Invoke-Expression

Register-ArgumentCompleter -Native -CommandName sqlgo -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $tokens = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
    $tokens = $tokens | Select-Object -Skip 1  # drop "sqlgo"

`)
	fmt.Fprintf(&b, "    $verbs = @(%s)\n", verbs)
	b.WriteString(`    $formats = @('csv','tsv','json','jsonl','markdown','table')
    $drivers = @('mssql','sqlserver','postgres','postgresql','mysql','sqlite','duckdb','odbc')
    $shells  = @('bash','zsh','fish','powershell','pwsh')

    # Find the verb (first non-flag token already typed).
    $verb = $null
    foreach ($t in $tokens) {
        if ($t -notlike '-*' -and $t -ne $wordToComplete) { $verb = $t; break }
    }

    # Value completion for the flag immediately before the cursor.
    $prev = if ($tokens.Count -ge 2) { $tokens[$tokens.Count - 2] } else { '' }
    switch ($prev) {
        '--format' { return $formats | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) } }
        '--driver' { return $drivers | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) } }
        { $_ -in '-c','--conn' } {
            $names = & sqlgo __complete conns 2>$null
            return $names | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
        }
    }

    if (-not $verb) {
        return $verbs | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterName', $_) }
    }

    $flags = @()
    $subs  = @()
    switch ($verb) {
`)
	for _, v := range topVerbs {
		fmt.Fprintf(&b, "        '%s' {\n", v)
		if subs, ok := verbSubs[v]; ok && len(subs) > 0 {
			fmt.Fprintf(&b, "            $subs  = @(%s)\n", strings.Join(quoteForPwsh(subs), ", "))
		}
		if flags, ok := verbFlags[v]; ok && len(flags) > 0 {
			fmt.Fprintf(&b, "            $flags = @(%s)\n", strings.Join(quoteForPwsh(flags), ", "))
		}
		b.WriteString("        }\n")
	}
	b.WriteString(`    }

    if ($verb -eq 'completion') {
        return $shells | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
    }

    # Subverbs come before flags if none has been typed yet.
    if ($subs.Count -gt 0) {
        $subSeen = $false
        foreach ($t in $tokens) {
            if ($t -eq $verb) { continue }
            if ($t -like '-*') { continue }
            if ($t -eq $wordToComplete) { continue }
            $subSeen = $true; break
        }
        if (-not $subSeen) {
            return $subs | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
        }
    }

    return $flags | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterName', $_) }
}
`)
	return b.String()
}

func quoteForPwsh(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return out
}
