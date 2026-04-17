package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/store"
)

// runConns dispatches `sqlgo conns <subcommand>`. Kept flat (one
// function per subcommand) so each subcommand owns its own flag set and
// usage line -- the shared runner.go machinery is only useful when you
// actually execute SQL, which none of these do.
func runConns(argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		connsUsage(stderr)
		return ExitUsage
	}
	sub, rest := argv[0], argv[1:]
	switch sub {
	case "list", "ls":
		return connsList(rest, stdout, stderr)
	case "show":
		return connsShow(rest, stdout, stderr)
	case "add":
		return connsAdd(rest, stdin, stderr, false)
	case "set":
		return connsAdd(rest, stdin, stderr, true)
	case "rm", "remove", "delete":
		return connsRm(rest, stderr)
	case "test":
		return connsTest(rest, stdin, stderr)
	case "import":
		return connsImport(rest, stdin, stderr)
	case "export":
		return connsExport(rest, stdout, stderr)
	case "help", "-h", "--help":
		connsUsage(stdout)
		return ExitOK
	}
	fmt.Fprintf(stderr, "sqlgo: conns: unknown subcommand %q\n", sub)
	connsUsage(stderr)
	return ExitUsage
}

func connsUsage(w io.Writer) {
	io.WriteString(w, `usage: sqlgo conns <subcommand> [flags]

subcommands:
  list                         list saved connections
  show    NAME                 show one connection (password masked)
  add     NAME --driver ...    create a new saved connection
  set     NAME --driver ...    update an existing connection (upsert)
  rm      NAME                 delete a saved connection
  test    NAME                 dial the connection and ping
  import  [-i FILE]            import connections from JSON (stdin if -i absent)
  export  [-o FILE]            export all connections as JSON; keyring-backed secrets stay as placeholders
`)
}

// connsList prints every saved connection using the standard output
// package so --format plays the same way it does for exec/export.
func connsList(argv []string, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("conns list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var format string
	fs.StringVar(&format, "format", "", "output format: csv|tsv|json|jsonl|markdown|table")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}

	st, err := openStore(context.Background())
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()

	conns, err := st.ListConnections(context.Background())
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}

	fmtSel := output.Table
	if format != "" {
		f, err := output.FormatFromName(format)
		if err != nil {
			stderrf(stderr, "%v", err)
			return ExitUsage
		}
		fmtSel = f
	} else if !terminalDetector(stdout) {
		fmtSel = output.TSV
	}

	cols := []db.Column{
		{Name: "name"}, {Name: "driver"}, {Name: "host"},
		{Name: "port"}, {Name: "user"}, {Name: "database"}, {Name: "ssh"},
	}
	rows := make([][]string, 0, len(conns))
	for _, c := range conns {
		port := ""
		if c.Port != 0 {
			port = fmt.Sprintf("%d", c.Port)
		}
		ssh := ""
		if c.SSH.Host != "" {
			ssh = c.SSH.Host
			if c.SSH.Port != 0 {
				ssh = fmt.Sprintf("%s:%d", c.SSH.Host, c.SSH.Port)
			}
		}
		rows = append(rows, []string{c.Name, c.Driver, c.Host, port, c.User, c.Database, ssh})
	}
	if err := output.Write(stdout, cols, rows, fmtSel); err != nil {
		stderrf(stderr, "write: %v", err)
		return ExitQuery
	}
	return ExitOK
}

// connsShow prints a single connection in a key: value layout. Password
// is always masked; this is for humans, not for piping into scripts
// (use `conns export` if you need structured output).
func connsShow(argv []string, stdout, stderr io.Writer) ExitCode {
	if len(argv) == 0 {
		stderrf(stderr, "conns show: NAME required")
		return ExitUsage
	}
	name := argv[0]
	st, err := openStore(context.Background())
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	c, err := st.GetConnection(context.Background(), name)
	if err != nil {
		if errors.Is(err, store.ErrConnectionNotFound) {
			stderrf(stderr, "no saved connection %q", name)
			return ExitConn
		}
		stderrf(stderr, "%v", err)
		return ExitConn
	}

	fmt.Fprintf(stdout, "name:     %s\n", c.Name)
	fmt.Fprintf(stdout, "driver:   %s\n", c.Driver)
	fmt.Fprintf(stdout, "host:     %s\n", c.Host)
	if c.Port != 0 {
		fmt.Fprintf(stdout, "port:     %d\n", c.Port)
	}
	fmt.Fprintf(stdout, "user:     %s\n", c.User)
	fmt.Fprintf(stdout, "database: %s\n", c.Database)
	fmt.Fprintf(stdout, "password: %s\n", maskPassword(c.Password))
	if len(c.Options) > 0 {
		keys := sortedKeys(c.Options)
		for _, k := range keys {
			fmt.Fprintf(stdout, "option:   %s=%s\n", k, c.Options[k])
		}
	}
	if c.SSH.Host != "" {
		fmt.Fprintf(stdout, "ssh host: %s\n", c.SSH.Host)
		if c.SSH.Port != 0 {
			fmt.Fprintf(stdout, "ssh port: %d\n", c.SSH.Port)
		}
		fmt.Fprintf(stdout, "ssh user: %s\n", c.SSH.User)
		if c.SSH.KeyPath != "" {
			fmt.Fprintf(stdout, "ssh key:  %s\n", c.SSH.KeyPath)
		}
		if c.SSH.Password != "" {
			fmt.Fprintf(stdout, "ssh pw:   %s\n", maskPassword(c.SSH.Password))
		}
	}
	return ExitOK
}

// connsAdd handles both `add` (refuses to clobber unless --force) and
// `set` (always upserts). Separated by the update bool so the flag
// surface and precedence are identical.
func connsAdd(argv []string, stdin io.Reader, stderr io.Writer, update bool) ExitCode {
	verb := "add"
	if update {
		verb = "set"
	}
	fs := flag.NewFlagSet("conns "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		driver, host, user, database string
		port                         int
		opts                         multiFlag
		force                        bool
		passwordStdin                bool
		useKeyring                   bool
		sshHost, sshUser, sshKeyPath string
		sshPort                      int
		sshPasswordStdin             bool
	)
	fs.StringVar(&driver, "driver", "", "driver name (mssql|postgres|mysql|sqlite|...)")
	fs.StringVar(&host, "host", "", "hostname")
	fs.IntVar(&port, "port", 0, "port")
	fs.StringVar(&user, "user", "", "username")
	fs.StringVar(&database, "database", "", "database / initial catalog")
	fs.Var(&opts, "option", "driver option as key=value (repeatable)")
	fs.BoolVar(&force, "force", false, "overwrite an existing connection (add only)")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin")
	fs.BoolVar(&useKeyring, "keyring", true, "store password in OS keyring if available")
	fs.StringVar(&sshHost, "ssh-host", "", "ssh jump host")
	fs.IntVar(&sshPort, "ssh-port", 0, "ssh port")
	fs.StringVar(&sshUser, "ssh-user", "", "ssh user")
	fs.StringVar(&sshKeyPath, "ssh-key", "", "path to ssh private key")
	fs.BoolVar(&sshPasswordStdin, "ssh-password-stdin", false, "read ssh password from stdin (after db password if both set)")
	name, rest, ok := splitNameArgs(argv)
	if !ok {
		stderrf(stderr, "conns %s: NAME required", verb)
		return ExitUsage
	}
	if err := fs.Parse(rest); err != nil {
		return ExitUsage
	}
	if driver == "" {
		stderrf(stderr, "conns %s: --driver required", verb)
		return ExitUsage
	}
	if _, err := db.Get(driver); err != nil {
		stderrf(stderr, "unknown driver %q (available: %s)", driver, strings.Join(db.Registered(), ", "))
		return ExitUsage
	}

	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()

	existing, getErr := st.GetConnection(ctx, name)
	exists := getErr == nil
	if getErr != nil && !errors.Is(getErr, store.ErrConnectionNotFound) {
		stderrf(stderr, "%v", getErr)
		return ExitConn
	}
	if !update && exists && !force {
		stderrf(stderr, "connection %q already exists (use --force to overwrite or `sqlgo conns set`)", name)
		return ExitUsage
	}
	if update && !exists {
		stderrf(stderr, "no saved connection %q (use `sqlgo conns add`)", name)
		return ExitConn
	}

	c := config.Connection{Name: name, Driver: driver}
	if exists {
		c = existing
		c.Name = name
		c.Driver = driver
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			c.Host = host
		case "port":
			c.Port = port
		case "user":
			c.User = user
		case "database":
			c.Database = database
		case "ssh-host":
			c.SSH.Host = sshHost
		case "ssh-port":
			c.SSH.Port = sshPort
		case "ssh-user":
			c.SSH.User = sshUser
		case "ssh-key":
			c.SSH.KeyPath = sshKeyPath
		}
	})
	if !exists {
		c.Host = host
		c.Port = port
		c.User = user
		c.Database = database
		c.SSH.Host = sshHost
		c.SSH.Port = sshPort
		c.SSH.User = sshUser
		c.SSH.KeyPath = sshKeyPath
	}
	if len(opts) > 0 {
		merged := map[string]string{}
		for k, v := range c.Options {
			merged[k] = v
		}
		for _, kv := range opts {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				stderrf(stderr, "--option %q: expected key=value", kv)
				return ExitUsage
			}
			if v == "" {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		if len(merged) == 0 {
			c.Options = nil
		} else {
			c.Options = merged
		}
	}

	// Password handling. Only consume stdin when the caller asked for it;
	// silent stdin-read would surprise anyone piping SQL in later.
	pw, pwSet, sshPW, sshPWSet, err := resolveConnectionPasswords(passwordStdin, sshPasswordStdin, stdin)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitUsage
	}
	secrets := secretStoreFactory()
	var cleanupAccounts []string
	if pwSet {
		if useKeyring {
			if err := secrets.Set(name, pw); err == nil {
				c.Password = secret.Placeholder
				cleanupAccounts = append(cleanupAccounts, name)
			} else {
				// Refuse the silent fallback: anyone who asked for keyring
				// storage deserves to know it failed, not to discover months
				// later that credentials are sitting in a plaintext store.
				// Force them to opt in to plaintext with --keyring=false.
				stderrf(stderr, "keyring unavailable: %v\nrerun with --keyring=false to store in plaintext", err)
				return ExitConn
			}
		} else {
			c.Password = pw
		}
	}

	if sshPWSet {
		if useKeyring {
			account := connectutil.SSHKeyringAccount(name)
			if err := secrets.Set(account, sshPW); err == nil {
				c.SSH.Password = secret.Placeholder
				cleanupAccounts = append(cleanupAccounts, account)
			} else {
				for _, created := range cleanupAccounts {
					_ = secrets.Delete(created)
				}
				stderrf(stderr, "keyring unavailable: %v\nrerun with --keyring=false to store in plaintext", err)
				return ExitConn
			}
		} else {
			c.SSH.Password = sshPW
		}
	}

	oldName := ""
	if exists {
		oldName = existing.Name
	}
	if err := st.SaveConnection(ctx, oldName, c); err != nil {
		for _, created := range cleanupAccounts {
			_ = secrets.Delete(created)
		}
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	if update {
		fmt.Fprintf(stderr, "sqlgo: updated %q\n", name)
	} else {
		fmt.Fprintf(stderr, "sqlgo: saved %q\n", name)
	}
	return ExitOK
}

func connsRm(argv []string, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("conns rm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var force bool
	fs.BoolVar(&force, "force", false, "do not error if the connection does not exist")
	name, rest, ok := splitNameArgs(argv)
	if !ok {
		stderrf(stderr, "conns rm: NAME required")
		return ExitUsage
	}
	if err := fs.Parse(rest); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	if err := st.DeleteConnection(ctx, name); err != nil {
		if errors.Is(err, store.ErrConnectionNotFound) && force {
			return ExitOK
		}
		if errors.Is(err, store.ErrConnectionNotFound) {
			stderrf(stderr, "no saved connection %q", name)
			return ExitConn
		}
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	// Best-effort keyring cleanup; missing entries are already a no-op in
	// the keyring package.
	secrets := secretStoreFactory()
	_ = secrets.Delete(name)
	_ = secrets.Delete(connectutil.SSHKeyringAccount(name))
	fmt.Fprintf(stderr, "sqlgo: removed %q\n", name)
	return ExitOK
}

// connsTest dials a saved connection and calls Ping. Non-zero exit on
// any failure so a CI job can gate on `sqlgo conns test prod`.
func connsTest(argv []string, stdin io.Reader, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("conns test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		timeout       time.Duration
		passwordStdin bool
	)
	fs.DurationVar(&timeout, "timeout", 10*time.Second, "dial+ping timeout")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin (overrides keyring/plaintext)")
	name, rest, ok := splitNameArgs(argv)
	if !ok {
		stderrf(stderr, "conns test: NAME required")
		return ExitUsage
	}
	if err := fs.Parse(rest); err != nil {
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	c, err := st.GetConnection(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrConnectionNotFound) {
			stderrf(stderr, "no saved connection %q", name)
			return ExitConn
		}
		stderrf(stderr, "%v", err)
		return ExitConn
	}

	runtime := runtimeDepsFactory()
	resolved, err := connectutil.ResolveSavedConnection(c, runtime)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	if passwordStdin {
		pw, err := readPasswordLine(stdin)
		if err != nil {
			stderrf(stderr, "read password: %v", err)
			return ExitUsage
		}
		resolved.Config.Password = pw
	} else if v, ok := os.LookupEnv("SQLGO_PASSWORD"); ok {
		resolved.Config.Password = v
	}
	conn, tunnel, err := connectutil.OpenResolvedConnection(ctx, resolved, runtime)
	if err != nil {
		stderrf(stderr, "connect: %v", err)
		return ExitConn
	}
	defer conn.Close()
	if tunnel != nil {
		defer tunnel.Close()
	}
	if err := conn.Ping(ctx); err != nil {
		stderrf(stderr, "ping: %v", err)
		return ExitConn
	}
	fmt.Fprintf(stderr, "sqlgo: %s ok\n", name)
	return ExitOK
}

func connsImport(argv []string, stdin io.Reader, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("conns import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var in string
	fs.StringVar(&in, "i", "", "input file (default stdin)")
	fs.StringVar(&in, "input", "", "input file (default stdin)")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}
	var r io.Reader = stdin
	if in != "" {
		fp, err := os.Open(in)
		if err != nil {
			stderrf(stderr, "open %s: %v", in, err)
			return ExitUsage
		}
		defer fp.Close()
		r = fp
	}
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	n, err := st.ImportJSON(ctx, r)
	if err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	fmt.Fprintf(stderr, "sqlgo: imported %d connection(s)\n", n)
	return ExitOK
}

func connsExport(argv []string, stdout, stderr io.Writer) ExitCode {
	fs := flag.NewFlagSet("conns export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var out string
	fs.StringVar(&out, "o", "", "output file (default stdout)")
	fs.StringVar(&out, "output", "", "output file (default stdout)")
	if err := fs.Parse(argv); err != nil {
		return ExitUsage
	}
	var w io.Writer = stdout
	if out != "" {
		fp, err := os.Create(out)
		if err != nil {
			stderrf(stderr, "create %s: %v", out, err)
			return ExitUsage
		}
		defer fp.Close()
		w = fp
	}
	ctx := context.Background()
	st, err := openStore(ctx)
	if err != nil {
		stderrf(stderr, "open store: %v", err)
		return ExitConn
	}
	defer st.Close()
	if err := st.ExportJSON(ctx, w); err != nil {
		stderrf(stderr, "%v", err)
		return ExitConn
	}
	return ExitOK
}

// parseInterleaved drives fs.Parse repeatedly so positionals and flags
// can mix in any order: stdlib flag stops at the first non-flag, which
// would silently drop `--limit 5` from `history search greeting --limit 5`.
// Returns the collected positionals.
func parseInterleaved(fs *flag.FlagSet, argv []string) ([]string, error) {
	var positional []string
	rest := argv
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// splitNameArgs extracts a leading NAME positional from argv when it is
// not flag-like, returning the remainder for flag parsing. Lets users
// write `conns add NAME --driver ...` (NAME first, flags after) which is
// what every example in the README uses; stdlib flag.Parse would
// otherwise stop at NAME and silently drop everything after it.
func splitNameArgs(argv []string) (name string, rest []string, ok bool) {
	for i, a := range argv {
		if strings.HasPrefix(a, "-") {
			continue
		}
		rest = append([]string{}, argv[:i]...)
		rest = append(rest, argv[i+1:]...)
		return a, rest, true
	}
	return "", nil, false
}

// multiFlag collects a repeatable string flag like --option k=v --option k2=v2.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// resolvePassword picks a password from --password-stdin or envName, in
// that order. Returns (value, set, err). set is false when neither
// source was populated so callers can leave the existing password alone
// on an update.
func resolvePassword(fromStdin bool, envName string, stdin io.Reader) (string, bool, error) {
	if fromStdin {
		pw, err := readPasswordLine(stdin)
		if err != nil {
			return "", false, err
		}
		return pw, true, nil
	}
	if v, ok := os.LookupEnv(envName); ok {
		return v, true, nil
	}
	return "", false, nil
}

func resolveConnectionPasswords(dbFromStdin, sshFromStdin bool, stdin io.Reader) (dbPW string, dbSet bool, sshPW string, sshSet bool, err error) {
	if dbFromStdin && sshFromStdin {
		lines, err := readPasswordLines(stdin, 2)
		if err != nil {
			return "", false, "", false, fmt.Errorf("read passwords: %w", err)
		}
		if len(lines) < 2 {
			return "", false, "", false, errors.New("read passwords: expected two newline-delimited values on stdin")
		}
		return lines[0], true, lines[1], true, nil
	}
	dbPW, dbSet, err = resolvePassword(dbFromStdin, "SQLGO_PASSWORD", stdin)
	if err != nil {
		return "", false, "", false, fmt.Errorf("read password: %w", err)
	}
	sshPW, sshSet, err = resolvePassword(sshFromStdin, "SQLGO_SSH_PASSWORD", stdin)
	if err != nil {
		return "", false, "", false, fmt.Errorf("read ssh password: %w", err)
	}
	return dbPW, dbSet, sshPW, sshSet, nil
}

func readPasswordLines(r io.Reader, want int) ([]string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s := strings.TrimRight(string(b), "\r\n")
	if s == "" {
		return nil, nil
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if want > 0 && len(lines) > want {
		lines = lines[:want]
	}
	return lines, nil
}

func maskPassword(p string) string {
	if p == "" {
		return ""
	}
	if p == secret.Placeholder {
		return "(keyring)"
	}
	return "***"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Small maps, so a simple insertion sort keeps the binary slim.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
