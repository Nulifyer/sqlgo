package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHandleQueryEventFormatsStructuredQueryError(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.activeConn = &config.Connection{Driver: "postgres"}
	a.layers = []Layer{newMainLayer()}
	sess := a.mainLayerPtr().ensureActiveTab()
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
}
