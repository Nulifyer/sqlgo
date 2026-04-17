package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/db/errinfo"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHandleQueryEventFormatsStructuredQueryErrorUsingQueryDriver(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.activeConn = &config.Connection{Driver: "mysql"}
	a.layers = []Layer{newMainLayer()}
	sess := a.mainLayerPtr().ensureActiveTab()
	sess.editor.buf.SetText("SELECT * FROM\n\"public\".\"widgetsz\"\nLIMIT 100")
	sess.lastQueryDriver = "postgres"
	sess.lastQueryBufferText = sess.editor.buf.Text()
	sess.lastQuerySentSQL = "SELECT * FROM\n\"public\".\"widgetsz\"\nLIMIT 100"

	err := fmt.Errorf("query: %w", &pgconn.PgError{
		Severity: "ERROR",
		Code:     "42P01",
		Message:  `relation "public.widgetsz" does not exist`,
		Position: 15,
	})
	a.handleQueryEvent(queryEvent{kind: evtResultSetDone, sess: sess, tab: sess.resultTab, err: err})

	if !strings.Contains(sess.lastErr, "SQLSTATE 42P01") {
		t.Fatalf("lastErr = %q, want SQLSTATE 42P01", sess.lastErr)
	}
	if sess.lastErrLine != 2 || sess.lastErrCol != 1 {
		t.Fatalf("location = (%d,%d), want (2,1)", sess.lastErrLine, sess.lastErrCol)
	}
	if !sess.editor.hasErrorLocation() {
		t.Fatal("editor marker should be set when the buffer still matches the run")
	}
}

func TestHandleQueryEventSkipsEditorMarkerWhenBufferChangedSinceRun(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	sess := a.mainLayerPtr().ensureActiveTab()
	sess.lastQueryBufferText = "SELECT *\nFORM widgets"
	sess.editor.buf.SetText("SELECT *\nFORM widgets\n-- edited after run")

	a.handleQueryEvent(queryEvent{
		kind: evtResultSetDone,
		sess: sess,
		tab:  sess.resultTab,
		err:  assertErr("query failed"),
		errInfo: errinfo.Info{
			Message:  "syntax error",
			Location: errinfo.Location{Line: 2, Column: 1},
		},
	})

	if sess.lastErrLine != 2 || sess.lastErrCol != 1 {
		t.Fatalf("location = (%d,%d), want (2,1)", sess.lastErrLine, sess.lastErrCol)
	}
	if sess.editor.hasErrorLocation() {
		t.Fatal("editor marker should stay cleared when the buffer changed after run start")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
