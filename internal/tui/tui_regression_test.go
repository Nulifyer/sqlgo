package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/store"
)

type regressionConn struct {
	closed    atomic.Bool
	listCalls atomic.Int32
	caps      db.Capabilities
}

func (c *regressionConn) Close() error                                 { c.closed.Store(true); return nil }
func (*regressionConn) Ping(context.Context) error                     { return nil }
func (*regressionConn) Query(context.Context, string) (db.Rows, error) { return &regressionRows{}, nil }
func (*regressionConn) Exec(context.Context, string, ...any) error     { return nil }
func (*regressionConn) Schema(context.Context) (*db.SchemaInfo, error) {
	return &db.SchemaInfo{}, nil
}
func (*regressionConn) Columns(context.Context, db.TableRef) ([]db.Column, error) { return nil, nil }
func (*regressionConn) Definition(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*regressionConn) Explain(context.Context, string) ([][]any, error) { return nil, nil }
func (*regressionConn) Driver() string                                   { return "test" }
func (c *regressionConn) Capabilities() db.Capabilities                  { return c.caps }
func (c *regressionConn) ListDatabases(context.Context) ([]string, error) {
	c.listCalls.Add(1)
	return []string{"main"}, nil
}
func (*regressionConn) SchemaForDatabase(context.Context, string) (*db.SchemaInfo, error) {
	return &db.SchemaInfo{}, nil
}

type regressionRows struct{}

func (*regressionRows) Columns() []db.Column { return nil }
func (*regressionRows) Next() bool           { return false }
func (*regressionRows) Scan() ([]any, error) { return nil, nil }
func (*regressionRows) Err() error           { return nil }
func (*regressionRows) Close() error         { return nil }
func (*regressionRows) NextResultSet() bool  { return false }

type failSetSecretStore struct {
	secret.Store
	failAccount string
}

func (s failSetSecretStore) Set(account, value string) error {
	if account == s.failAccount {
		return errors.New("injected keyring write failure")
	}
	return s.Store.Set(account, value)
}

func openRegressionStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenAt(context.Background(), filepath.Join(t.TempDir(), "sqlgo.db"))
	if err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			t.Skip("sqlite_fts5 build tag required")
		}
		t.Fatal(err)
	}
	return st
}

func TestKeyringChangesRollbackRestoresExistingAndDeletesNewAccounts(t *testing.T) {
	secrets := secret.NewMemory()
	if err := secrets.Set("existing", "old-secret"); err != nil {
		t.Fatal(err)
	}
	changes := &keyringChanges{store: secrets}
	if err := changes.set("existing", "new-secret"); err != nil {
		t.Fatal(err)
	}
	if err := changes.set("new", "temporary-secret"); err != nil {
		t.Fatal(err)
	}

	if err := changes.rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := secrets.Get("existing")
	if err != nil {
		t.Fatal(err)
	}
	if got != "old-secret" {
		t.Fatalf("restored existing secret = %q, want old-secret", got)
	}
	if _, err := secrets.Get("new"); !secret.IsNotFound(err) {
		t.Fatalf("new account survived rollback: %v", err)
	}
}

func TestConnCoordinatorSerializesLeases(t *testing.T) {
	var coordinator connCoordinator
	releaseFirst := coordinator.acquire()
	acquired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		releaseSecond := coordinator.acquire()
		close(acquired)
		releaseSecond()
		close(done)
	}()

	select {
	case <-acquired:
		t.Fatal("second connection lease acquired before first released")
	case <-time.After(20 * time.Millisecond):
	}
	releaseFirst()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second connection lease did not acquire after release")
	}
}

func TestExplainUsesSharedConnectionCoordinator(t *testing.T) {
	a := &app{conn: &regressionConn{caps: db.Capabilities{ExplainFormat: db.ExplainFormatSQLiteRows}}}
	release := a.connOps.acquire()
	done := make(chan struct{})
	go func() {
		_, _ = a.runExplain(a.conn, "", "select 1")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("explain bypassed the shared connection coordinator")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("explain did not resume after the connection lease was released")
	}
}

func TestFinishConnectPreservesEditorText(t *testing.T) {
	m := newMainLayer()
	sess := m.ensureActiveTab()
	sess.editor.buf.SetText("select important_work")
	a := &app{
		layers:     []Layer{m},
		asyncCh:    make(chan func(*app), 8),
		resultCh:   make(chan queryEvent, 8),
		connectGen: 7,
	}

	a.finishConnect(7, config.Connection{Name: "next"}, &regressionConn{}, nil, nil)

	if got := sess.editor.buf.Text(); got != "select important_work" {
		t.Fatalf("editor text = %q, want preserved", got)
	}
}

func TestFinishConnectDropsStaleAttempt(t *testing.T) {
	m := newMainLayer()
	active := &regressionConn{}
	stale := &regressionConn{}
	a := &app{layers: []Layer{m}, conn: active, connectGen: 3}

	a.finishConnect(2, config.Connection{Name: "stale"}, stale, nil, nil)

	if a.conn != active {
		t.Fatal("stale connection replaced active connection")
	}
	if !stale.closed.Load() {
		t.Fatal("stale successful connection was not closed")
	}
}

func TestDirtyTabCloseRequiresConfirmation(t *testing.T) {
	m := newMainLayer()
	sess := m.ensureActiveTab()
	sess.editor.buf.SetText("select unsaved")
	a := &app{layers: []Layer{m}}

	m.requestCloseTab(a, 0)
	if len(m.sessions) != 1 {
		t.Fatal("dirty tab closed before confirmation")
	}
	layer, ok := a.topLayer().(*dirtyConfirmLayer)
	if !ok {
		t.Fatalf("top layer = %T, want *dirtyConfirmLayer", a.topLayer())
	}
	layer.HandleKey(a, Key{Kind: KeyRune, Rune: 'y'})
	if len(m.sessions) != 0 {
		t.Fatal("dirty tab remained after discard confirmation")
	}
}

func TestClosingRunningTabCancelsAndInvalidatesRun(t *testing.T) {
	m := newMainLayer()
	sess := m.ensureActiveTab()
	ctx, cancel := context.WithCancel(context.Background())
	sess.running = true
	sess.runGen = 2
	sess.cancel = cancel

	m.closeTab(0)

	if ctx.Err() != context.Canceled {
		t.Fatal("closing a running tab did not cancel its query")
	}
	if sess.runGen != 3 {
		t.Fatalf("closed tab run generation = %d, want 3", sess.runGen)
	}
	if sess.running || sess.cancel != nil {
		t.Fatal("closed tab retained active runner state")
	}
}

func TestDirtyQuitRequiresConfirmation(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().editor.buf.SetText("select unsaved")
	a := &app{layers: []Layer{m}}

	a.requestQuit()
	if a.quit {
		t.Fatal("quit set before dirty confirmation")
	}
	layer, ok := a.topLayer().(*dirtyConfirmLayer)
	if !ok {
		t.Fatalf("top layer = %T, want *dirtyConfirmLayer", a.topLayer())
	}
	layer.HandleKey(a, Key{Kind: KeyRune, Rune: 'y'})
	if !a.quit {
		t.Fatal("quit not set after discard confirmation")
	}
}

func TestStaleQueryEventCannotFinishNewerRun(t *testing.T) {
	m := newMainLayer()
	sess := m.ensureActiveTab()
	sess.runGen = 4
	sess.running = true
	a := &app{layers: []Layer{m}}

	a.handleQueryEvent(queryEvent{kind: evtDone, sess: sess, runGen: 3})

	if !sess.running {
		t.Fatal("stale query event finished the active run")
	}
}

func TestInvalidateSchemaLoadCancelsAndAdvancesGeneration(t *testing.T) {
	m := newMainLayer()
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{
		layers:        []Layer{m},
		schemaLoading: true,
		schemaGen:     9,
		schemaCancel:  cancel,
	}

	a.invalidateSchemaLoad()

	if ctx.Err() != context.Canceled {
		t.Fatal("schema load context was not canceled")
	}
	if a.schemaLoading {
		t.Fatal("schema loading remained active after invalidation")
	}
	if a.schemaGen != 10 {
		t.Fatalf("schema generation = %d, want 10", a.schemaGen)
	}
	if a.schemaCancel != nil {
		t.Fatal("schema cancel function was not cleared")
	}
}

func TestComputeLayoutNeverReturnsNegativeGeometry(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {10, 5}, {39, 9}} {
		p := computeLayout(size[0], size[1])
		for name, r := range map[string]rect{
			"explorer": p.explorer, "query": p.query, "results": p.results, "status": p.status,
		} {
			if r.W < 0 || r.H < 0 {
				t.Fatalf("%dx%d %s geometry = %#v", size[0], size[1], name, r)
			}
		}
	}
}

func TestConnectionPickerScrollsSelectionIntoView(t *testing.T) {
	conns := make([]config.Connection, 24)
	for i := range conns {
		conns[i].Name = "connection"
	}
	p := newPicker(conns)
	p.selected = len(conns) - 1
	b := newCellbuf(80, 16)

	p.draw(b, 80, 16)

	if p.scroll == 0 {
		t.Fatal("picker scroll remained zero for an off-screen selection")
	}
	if p.selected < p.scroll || p.selected >= p.scroll+p.lastVisible {
		t.Fatalf("selection %d not in viewport [%d,%d)", p.selected, p.scroll, p.scroll+p.lastVisible)
	}
}

func TestRunningQueryHintsExposePanelIndependentCancel(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().running = true
	m.focus = FocusQuery
	a := &app{layers: []Layer{m}, conn: &regressionConn{}}

	if got := m.Hints(a); !strings.Contains(got, "F6=cancel") {
		t.Fatalf("query hints = %q, want F6 cancellation", got)
	}
}

func TestCatalogFetchDoesNotBlockBehindRunningQuery(t *testing.T) {
	m := newMainLayer()
	m.ensureActiveTab().running = true
	conn := &regressionConn{caps: db.Capabilities{SupportsCrossDatabase: true}}
	a := &app{layers: []Layer{m}, conn: conn}

	a.openCatalogLayer()

	if got := conn.listCalls.Load(); got != 0 {
		t.Fatalf("ListDatabases calls = %d, want 0 while a query is running", got)
	}
	if !strings.Contains(m.status, "F6") {
		t.Fatalf("status = %q, want cancellation direction", m.status)
	}
}

func TestPersistConnectionMovesNewSSHPasswordWhenDBPasswordIsPlaceholder(t *testing.T) {
	ctx := context.Background()
	st := openRegressionStore(t)
	t.Cleanup(func() { _ = st.Close() })
	secrets := secret.NewMemory()
	a := &app{store: st, secrets: secrets, secretsAvailable: true}
	c := config.Connection{
		Name: "prod", Driver: "postgres", Password: secret.Placeholder,
		SSH: config.SSHTunnel{Host: "jump", Port: 22, User: "ops", Password: "new-ssh-secret"},
	}

	usedKeyring, err := a.persistConnection(ctx, "", c)
	if err != nil {
		t.Fatal(err)
	}
	if !usedKeyring {
		t.Fatal("persistConnection did not report keyring use")
	}
	row, err := st.GetConnection(ctx, c.Name)
	if err != nil {
		t.Fatal(err)
	}
	if row.SSH.Password != secret.Placeholder {
		t.Fatalf("stored SSH password = %q, want keyring placeholder", row.SSH.Password)
	}
	got, err := secrets.Get(connectutil.SSHKeyringAccount(c.Name))
	if err != nil {
		t.Fatal(err)
	}
	if got != "new-ssh-secret" {
		t.Fatalf("keyring SSH password = %q, want new secret", got)
	}
}

func TestPersistPlaceholderBranchRestoresExistingSSHSecretAfterStoreFailure(t *testing.T) {
	ctx := context.Background()
	st := openRegressionStore(t)
	account := connectutil.SSHKeyringAccount("prod")
	secrets := secret.NewMemory()
	if err := secrets.Set(account, "old-ssh-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	a := &app{store: st, secrets: secrets, secretsAvailable: true}
	c := config.Connection{
		Name: "prod", Driver: "postgres", Password: secret.Placeholder,
		SSH: config.SSHTunnel{Host: "jump", Port: 22, User: "ops", Password: "new-ssh-secret"},
	}

	if _, err := a.persistConnection(ctx, "prod", c); err == nil {
		t.Fatal("persistConnection succeeded with a closed store")
	}
	got, err := secrets.Get(account)
	if err != nil {
		t.Fatal(err)
	}
	if got != "old-ssh-secret" {
		t.Fatalf("keyring SSH password after rollback = %q, want old secret", got)
	}
}

func TestPersistOrdinaryBranchRestoresExistingSecretsAfterStoreFailure(t *testing.T) {
	ctx := context.Background()
	st := openRegressionStore(t)
	sshAccount := connectutil.SSHKeyringAccount("prod")
	secrets := secret.NewMemory()
	if err := secrets.Set("prod", "old-db-secret"); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set(sshAccount, "old-ssh-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	a := &app{store: st, secrets: secrets, secretsAvailable: true}
	c := config.Connection{
		Name: "prod", Driver: "postgres", Password: "new-db-secret",
		SSH: config.SSHTunnel{Host: "jump", Port: 22, User: "ops", Password: "new-ssh-secret"},
	}

	if _, err := a.persistConnection(ctx, "prod", c); err == nil {
		t.Fatal("persistConnection succeeded with a closed store")
	}
	for account, want := range map[string]string{
		"prod": "old-db-secret", sshAccount: "old-ssh-secret",
	} {
		got, err := secrets.Get(account)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("keyring account %q after rollback = %q, want %q", account, got, want)
		}
	}
}

func TestPersistOrdinaryBranchRestoresDBSecretAfterSSHWriteFailure(t *testing.T) {
	ctx := context.Background()
	st := openRegressionStore(t)
	t.Cleanup(func() { _ = st.Close() })
	sshAccount := connectutil.SSHKeyringAccount("prod")
	memory := secret.NewMemory()
	if err := memory.Set("prod", "old-db-secret"); err != nil {
		t.Fatal(err)
	}
	if err := memory.Set(sshAccount, "old-ssh-secret"); err != nil {
		t.Fatal(err)
	}
	secrets := failSetSecretStore{Store: memory, failAccount: sshAccount}
	a := &app{store: st, secrets: secrets, secretsAvailable: true}
	c := config.Connection{
		Name: "prod", Driver: "postgres", Password: "new-db-secret",
		SSH: config.SSHTunnel{Host: "jump", Port: 22, User: "ops", Password: "new-ssh-secret"},
	}

	if _, err := a.persistConnection(ctx, "prod", c); err == nil {
		t.Fatal("persistConnection succeeded after the injected SSH keyring failure")
	}
	for account, want := range map[string]string{
		"prod": "old-db-secret", sshAccount: "old-ssh-secret",
	} {
		got, err := memory.Get(account)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("keyring account %q after rollback = %q, want %q", account, got, want)
		}
	}
}
