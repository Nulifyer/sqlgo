package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"github.com/Nulifyer/sqlgo/internal/store"
)

func init() {
	db.Register(testRegisteredDriver{name: "cli-test-driver"})
}

func TestDispatchUnknownVerbReturnsUsage(t *testing.T) {
	var stderr bytes.Buffer
	code := Dispatch([]string{"nope"}, bytes.NewBuffer(nil), bytes.NewBuffer(nil), &stderr)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`unknown verb "nope"`)) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestResolveQueryPrecedence(t *testing.T) {
	restoreTerminal := stubTerminalDetector(t, false)
	defer restoreTerminal()

	path := filepath.Join(t.TempDir(), "query.sql")
	if err := os.WriteFile(path, []byte("select from file"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tests := []struct {
		name  string
		flags commonFlags
		stdin string
		want  string
	}{
		{
			name:  "explicit query",
			flags: commonFlags{Query: "select inline", File: path},
			stdin: "select stdin",
			want:  "select inline",
		},
		{
			name:  "file",
			flags: commonFlags{File: path},
			stdin: "select stdin",
			want:  "select from file",
		},
		{
			name:  "auto stdin when piped",
			flags: commonFlags{},
			stdin: "select stdin",
			want:  "select stdin",
		},
	}

	for _, tc := range tests {
		got, err := tc.flags.resolveQuery(bytes.NewBufferString(tc.stdin))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildRunOptionsSelectsOutputFormat(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		output    string
		tty       bool
		defaultFM output.Format
		want      output.Format
	}{
		{
			name:      "pipe defaults to tsv",
			tty:       false,
			defaultFM: output.CSV,
			want:      output.TSV,
		},
		{
			name:      "path infers format",
			output:    filepath.Join(t.TempDir(), "report.jsonl"),
			tty:       true,
			defaultFM: output.CSV,
			want:      output.JSONL,
		},
		{
			name:      "explicit format wins",
			format:    "markdown",
			output:    filepath.Join(t.TempDir(), "report.csv"),
			tty:       false,
			defaultFM: output.CSV,
			want:      output.Markdown,
		},
	}

	for _, tc := range tests {
		restoreTerminal := stubTerminalDetector(t, tc.tty)
		opts, code, err := buildRunOptions(
			&commonFlags{
				DSN:    "postgres://user:pass@db.local:5432/app",
				Query:  "select 1",
				Format: tc.format,
				Output: tc.output,
			},
			bytes.NewBuffer(nil),
			bytes.NewBuffer(nil),
			bytes.NewBuffer(nil),
			tc.defaultFM,
		)
		restoreTerminal()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if code != ExitOK {
			t.Fatalf("%s: code = %d, want %d", tc.name, code, ExitOK)
		}
		if opts.format != tc.want {
			t.Fatalf("%s: format = %v, want %v", tc.name, opts.format, tc.want)
		}
		if opts.outClose != nil {
			_ = opts.outClose()
		}
	}
}

func TestBuildRunOptionsSavedConnectionNotFound(t *testing.T) {
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionErr: store.ErrConnectionNotFound,
	})
	defer restoreStore()

	_, code, err := buildRunOptions(
		&commonFlags{Conn: "missing", Query: "select 1"},
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		output.Table,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if code != ExitConn {
		t.Fatalf("code = %d, want %d", code, ExitConn)
	}
}

func TestBuildRunOptionsSavedConnectionUsesOpenWithAndPasswordPrecedence(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("other", "stored-pass"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionValue: config.Connection{
			Name:      "other",
			Profile:   "profile-a",
			Transport: "transport-a",
			Host:      "db.internal",
			Port:      15432,
			User:      "reader",
			Password:  secret.Placeholder,
			Database:  "app",
		},
	})
	defer restoreStore()

	var openWithCfg db.Config
	restoreRuntime := stubRuntimeDepsFactory(t, connectutil.RuntimeDeps{
		Secrets: mem,
		GetProfile: func(name string) (db.Profile, bool) {
			return db.Profile{Name: name}, name == "profile-a"
		},
		GetTransport: func(name string) (db.Transport, bool) {
			return db.Transport{Name: name}, name == "transport-a"
		},
		OpenWith: func(ctx context.Context, p db.Profile, tr db.Transport, cfg db.Config) (db.Conn, error) {
			openWithCfg = cfg
			return &fakeConn{}, nil
		},
	})
	defer restoreRuntime()

	t.Setenv("SQLGO_PASSWORD", "env-pass")

	opts, code, err := buildRunOptions(
		&commonFlags{Conn: "other", Query: "select 1"},
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		output.Table,
	)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}
	if code != ExitOK {
		t.Fatalf("code = %d, want %d", code, ExitOK)
	}
	if opts.cfg.Password != "env-pass" {
		t.Fatalf("opts.cfg.Password = %q, want env override", opts.cfg.Password)
	}

	conn, closer, err := opts.openConn(context.Background())
	if err != nil {
		t.Fatalf("openConn: %v", err)
	}
	if conn == nil {
		t.Fatal("expected conn")
	}
	if closer != nil {
		t.Fatalf("closer = %v, want nil", closer)
	}
	if openWithCfg.Password != "env-pass" {
		t.Fatalf("openWith password = %q, want env-pass", openWithCfg.Password)
	}
	if openWithCfg.Host != "db.internal" || openWithCfg.Port != 15432 {
		t.Fatalf("openWith cfg = %+v", openWithCfg)
	}
}

func TestBuildRunOptionsPasswordStdinBeatsEnv(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("saved", "stored-pass"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionValue: config.Connection{
			Name:     "saved",
			Driver:   "cli-test-driver",
			Host:     "db.internal",
			Password: secret.Placeholder,
		},
	})
	defer restoreStore()
	restoreRuntime := stubRuntimeDepsFactory(t, connectutil.RuntimeDeps{
		Secrets: mem,
		GetDriver: func(name string) (db.Driver, error) {
			return testRegisteredDriver{name: name}, nil
		},
	})
	defer restoreRuntime()

	t.Setenv("SQLGO_PASSWORD", "env-pass")

	opts, code, err := buildRunOptions(
		&commonFlags{Conn: "saved", Query: "select 1", PasswordStdin: true},
		bytes.NewBufferString("stdin-pass\n"),
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		output.Table,
	)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}
	if code != ExitOK {
		t.Fatalf("code = %d, want %d", code, ExitOK)
	}
	if opts.cfg.Password != "stdin-pass" {
		t.Fatalf("opts.cfg.Password = %q, want stdin-pass", opts.cfg.Password)
	}
}

func TestBuildRunOptionsSavedConnectionUsesSSHTunnel(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("saved", "db-pass"); err != nil {
		t.Fatalf("set db secret: %v", err)
	}
	if err := mem.Set(connectutil.SSHKeyringAccount("saved"), "ssh-pass"); err != nil {
		t.Fatalf("set ssh secret: %v", err)
	}
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionValue: config.Connection{
			Name:     "saved",
			Driver:   "cli-test-driver",
			Host:     "db.internal",
			Port:     5432,
			User:     "reader",
			Password: secret.Placeholder,
			SSH: config.SSHTunnel{
				Host:     "jump.internal",
				Port:     2222,
				User:     "ssh-user",
				Password: secret.Placeholder,
			},
		},
	})
	defer restoreStore()

	tunnelErr := errors.New("tunnel invoked")
	var openedTunnel sshtunnel.Config
	restoreRuntime := stubRuntimeDepsFactory(t, connectutil.RuntimeDeps{
		Secrets: mem,
		GetDriver: func(name string) (db.Driver, error) {
			return testRegisteredDriver{name: name}, nil
		},
		OpenTunnel: func(cfg sshtunnel.Config) (*sshtunnel.Tunnel, error) {
			openedTunnel = cfg
			return nil, tunnelErr
		},
	})
	defer restoreRuntime()

	opts, code, err := buildRunOptions(
		&commonFlags{Conn: "saved", Query: "select 1"},
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		bytes.NewBuffer(nil),
		output.Table,
	)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}
	if code != ExitOK {
		t.Fatalf("code = %d, want %d", code, ExitOK)
	}
	if _, _, err := opts.openConn(context.Background()); !errors.Is(err, tunnelErr) {
		t.Fatalf("openConn error = %v, want %v", err, tunnelErr)
	}
	if openedTunnel.SSHHost != "jump.internal" || openedTunnel.SSHPort != 2222 {
		t.Fatalf("openedTunnel = %+v", openedTunnel)
	}
	if openedTunnel.SSHPassword != "ssh-pass" {
		t.Fatalf("ssh password = %q, want ssh-pass", openedTunnel.SSHPassword)
	}
	if openedTunnel.TargetHost != "db.internal" || openedTunnel.TargetPort != 5432 {
		t.Fatalf("openedTunnel target = %+v", openedTunnel)
	}
}

func TestConnsAddStoresDBAndSSHSecretsInKeyring(t *testing.T) {
	mem := secret.NewMemory()
	fakeStore := &fakeCLIStore{}
	restoreStore := stubOpenStore(t, fakeStore)
	defer restoreStore()
	restoreSecrets := stubSecretStoreFactory(t, mem)
	defer restoreSecrets()

	var stderr bytes.Buffer
	code := connsAdd([]string{
		"demo",
		"--driver", "cli-test-driver",
		"--host", "db.internal",
		"--user", "reader",
		"--password-stdin",
		"--ssh-host", "jump.internal",
		"--ssh-user", "jump",
		"--ssh-password-stdin",
	}, bytes.NewBufferString("db-secret\nssh-secret\n"), &stderr, false)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if len(fakeStore.saveCalls) != 1 {
		t.Fatalf("save calls = %d, want 1", len(fakeStore.saveCalls))
	}
	saved := fakeStore.saveCalls[0].conn
	if saved.Password != secret.Placeholder {
		t.Fatalf("db password = %q, want placeholder", saved.Password)
	}
	if saved.SSH.Password != secret.Placeholder {
		t.Fatalf("ssh password = %q, want placeholder", saved.SSH.Password)
	}
	if got, err := mem.Get("demo"); err != nil || got != "db-secret" {
		t.Fatalf("db keyring = %q, %v", got, err)
	}
	if got, err := mem.Get(connectutil.SSHKeyringAccount("demo")); err != nil || got != "ssh-secret" {
		t.Fatalf("ssh keyring = %q, %v", got, err)
	}
}

func TestConnsRmDeletesDBAndSSHSecrets(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("demo", "db-secret"); err != nil {
		t.Fatalf("set db secret: %v", err)
	}
	if err := mem.Set(connectutil.SSHKeyringAccount("demo"), "ssh-secret"); err != nil {
		t.Fatalf("set ssh secret: %v", err)
	}
	fakeStore := &fakeCLIStore{}
	restoreStore := stubOpenStore(t, fakeStore)
	defer restoreStore()
	restoreSecrets := stubSecretStoreFactory(t, mem)
	defer restoreSecrets()

	var stderr bytes.Buffer
	code := connsRm([]string{"demo"}, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if len(fakeStore.deleteCalls) != 1 || fakeStore.deleteCalls[0] != "demo" {
		t.Fatalf("delete calls = %+v", fakeStore.deleteCalls)
	}
	if _, err := mem.Get("demo"); err == nil {
		t.Fatal("db secret still present")
	}
	if _, err := mem.Get(connectutil.SSHKeyringAccount("demo")); err == nil {
		t.Fatal("ssh secret still present")
	}
}

func TestConnsTestUsesOpenWithForSavedConnection(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("other", "db-pass"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionValue: config.Connection{
			Name:      "other",
			Profile:   "profile-a",
			Transport: "transport-a",
			Host:      "db.internal",
			Port:      15432,
			Password:  secret.Placeholder,
		},
	})
	defer restoreStore()

	fc := &fakeConn{}
	var openWithCfg db.Config
	restoreRuntime := stubRuntimeDepsFactory(t, connectutil.RuntimeDeps{
		Secrets: mem,
		GetProfile: func(name string) (db.Profile, bool) {
			return db.Profile{Name: name}, true
		},
		GetTransport: func(name string) (db.Transport, bool) {
			return db.Transport{Name: name}, true
		},
		OpenWith: func(ctx context.Context, p db.Profile, tr db.Transport, cfg db.Config) (db.Conn, error) {
			openWithCfg = cfg
			return fc, nil
		},
	})
	defer restoreRuntime()

	var stderr bytes.Buffer
	code := connsTest([]string{"other"}, bytes.NewBuffer(nil), &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if fc.pingCalls != 1 {
		t.Fatalf("ping calls = %d, want 1", fc.pingCalls)
	}
	if openWithCfg.Password != "db-pass" {
		t.Fatalf("openWith password = %q, want db-pass", openWithCfg.Password)
	}
}

func TestConnsTestUsesSSHTunnelSettings(t *testing.T) {
	mem := secret.NewMemory()
	if err := mem.Set("saved", "db-pass"); err != nil {
		t.Fatalf("set db secret: %v", err)
	}
	if err := mem.Set(connectutil.SSHKeyringAccount("saved"), "ssh-pass"); err != nil {
		t.Fatalf("set ssh secret: %v", err)
	}
	restoreStore := stubOpenStore(t, &fakeCLIStore{
		getConnectionValue: config.Connection{
			Name:     "saved",
			Driver:   "cli-test-driver",
			Host:     "db.internal",
			Port:     5432,
			Password: secret.Placeholder,
			SSH: config.SSHTunnel{
				Host:     "jump.internal",
				Port:     2222,
				User:     "jump",
				Password: secret.Placeholder,
			},
		},
	})
	defer restoreStore()

	tunnelErr := errors.New("tunnel invoked")
	var openedTunnel sshtunnel.Config
	restoreRuntime := stubRuntimeDepsFactory(t, connectutil.RuntimeDeps{
		Secrets: mem,
		GetDriver: func(name string) (db.Driver, error) {
			return testRegisteredDriver{name: name}, nil
		},
		OpenTunnel: func(cfg sshtunnel.Config) (*sshtunnel.Tunnel, error) {
			openedTunnel = cfg
			return nil, tunnelErr
		},
	})
	defer restoreRuntime()

	var stderr bytes.Buffer
	code := connsTest([]string{"saved"}, bytes.NewBuffer(nil), &stderr)
	if code != ExitConn {
		t.Fatalf("code = %d, want %d", code, ExitConn)
	}
	if openedTunnel.SSHPassword != "ssh-pass" {
		t.Fatalf("ssh password = %q, want ssh-pass", openedTunnel.SSHPassword)
	}
	if openedTunnel.TargetHost != "db.internal" || openedTunnel.TargetPort != 5432 {
		t.Fatalf("openedTunnel = %+v", openedTunnel)
	}
}

type fakeCLIStore struct {
	getConnectionValue config.Connection
	getConnectionErr   error
	saveCalls          []saveCall
	deleteCalls        []string
}

type saveCall struct {
	oldName string
	conn    config.Connection
}

func (f *fakeCLIStore) Close() error { return nil }

func (f *fakeCLIStore) GetConnection(ctx context.Context, name string) (config.Connection, error) {
	if f.getConnectionErr != nil {
		return config.Connection{}, f.getConnectionErr
	}
	if f.getConnectionValue.Name == "" {
		return config.Connection{}, store.ErrConnectionNotFound
	}
	return f.getConnectionValue, nil
}

func (f *fakeCLIStore) ListConnections(ctx context.Context) ([]config.Connection, error) {
	return nil, nil
}

func (f *fakeCLIStore) SaveConnection(ctx context.Context, oldName string, c config.Connection) error {
	f.saveCalls = append(f.saveCalls, saveCall{oldName: oldName, conn: c})
	f.getConnectionValue = c
	return nil
}

func (f *fakeCLIStore) DeleteConnection(ctx context.Context, name string) error {
	f.deleteCalls = append(f.deleteCalls, name)
	return nil
}

func (f *fakeCLIStore) ExportJSON(ctx context.Context, w io.Writer) error { return nil }

func (f *fakeCLIStore) ImportJSON(ctx context.Context, r io.Reader) (int, error) { return 0, nil }

func (f *fakeCLIStore) ClearHistory(ctx context.Context, connectionName string) (int64, error) {
	return 0, nil
}

func (f *fakeCLIStore) ListRecentHistory(ctx context.Context, connectionName string, limit int) ([]store.HistoryEntry, error) {
	return nil, nil
}

func (f *fakeCLIStore) SearchHistory(ctx context.Context, connectionName, q string, limit int) ([]store.HistoryEntry, error) {
	return nil, nil
}

func (f *fakeCLIStore) RecordHistory(ctx context.Context, e store.HistoryEntry) error { return nil }

type testRegisteredDriver struct {
	name string
	conn db.Conn
}

func (d testRegisteredDriver) Name() string { return d.name }

func (d testRegisteredDriver) Capabilities() db.Capabilities { return db.Capabilities{} }

func (d testRegisteredDriver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	if d.conn != nil {
		return d.conn, nil
	}
	return &fakeConn{}, nil
}

type fakeConn struct {
	pingCalls int
	pingErr   error
}

func (f *fakeConn) Close() error { return nil }

func (f *fakeConn) Ping(ctx context.Context) error {
	f.pingCalls++
	return f.pingErr
}

func (f *fakeConn) Query(ctx context.Context, sql string) (db.Rows, error) {
	return &fakeRows{
		cols: []db.Column{{Name: "v"}},
		data: [][]any{{"ok"}},
	}, nil
}

func (f *fakeConn) Exec(ctx context.Context, sql string, args ...any) error { return nil }

func (f *fakeConn) Schema(ctx context.Context) (*db.SchemaInfo, error) { return nil, nil }

func (f *fakeConn) Columns(ctx context.Context, t db.TableRef) ([]db.Column, error) { return nil, nil }

func (f *fakeConn) Definition(ctx context.Context, kind, schema, name string) (string, error) {
	return "", nil
}

func (f *fakeConn) Explain(ctx context.Context, sql string) ([][]any, error) { return nil, nil }

func (f *fakeConn) Driver() string { return "cli-test-driver" }

func (f *fakeConn) Capabilities() db.Capabilities { return db.Capabilities{} }

type fakeRows struct {
	cols []db.Column
	data [][]any
	idx  int
}

func (f *fakeRows) Columns() []db.Column { return f.cols }

func (f *fakeRows) Next() bool {
	if f.idx >= len(f.data) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeRows) Scan() ([]any, error) {
	return f.data[f.idx-1], nil
}

func (f *fakeRows) Err() error { return nil }

func (f *fakeRows) Close() error { return nil }

func (f *fakeRows) NextResultSet() bool { return false }

func stubOpenStore(t *testing.T, st cliStore) func() {
	t.Helper()
	old := openStoreFn
	openStoreFn = func(ctx context.Context) (cliStore, error) {
		return st, nil
	}
	return func() { openStoreFn = old }
}

func stubSecretStoreFactory(t *testing.T, st secret.Store) func() {
	t.Helper()
	old := secretStoreFactory
	secretStoreFactory = func() secret.Store { return st }
	return func() { secretStoreFactory = old }
}

func stubRuntimeDepsFactory(t *testing.T, deps connectutil.RuntimeDeps) func() {
	t.Helper()
	old := runtimeDepsFactory
	runtimeDepsFactory = func() connectutil.RuntimeDeps { return deps }
	return func() { runtimeDepsFactory = old }
}

func stubTerminalDetector(t *testing.T, isTTY bool) func() {
	t.Helper()
	old := terminalDetector
	terminalDetector = func(v any) bool { return isTTY }
	return func() { terminalDetector = old }
}
