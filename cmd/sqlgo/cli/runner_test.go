package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/output"
	"github.com/Nulifyer/sqlgo/internal/store"
)

func TestRunContinueOnErrorQueriesLaterTopLevelStatements(t *testing.T) {
	queryErr := errors.New("middle statement failed")
	conn := &runnerConn{}
	conn.query = func(sql string) (db.Rows, error) {
		switch sql {
		case "SELECT 'first;value';":
			return newRunnerRows([]runnerResult{{rows: [][]any{{"first"}}}}), nil
		case "SELECT bad;":
			return nil, queryErr
		case "SELECT last;":
			return newRunnerRows([]runnerResult{{rows: [][]any{{"last"}}}}), nil
		default:
			return nil, fmt.Errorf("unexpected query %q", sql)
		}
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), runnerOpts(conn, "SELECT 'first;value'; SELECT bad; SELECT last;", output.JSONL, &stdout, &stderr))
	if code != ExitQuery {
		t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitQuery, stderr.String())
	}
	wantQueries := []string{"SELECT 'first;value';", "SELECT bad;", "SELECT last;"}
	if fmt.Sprint(conn.queries) != fmt.Sprint(wantQueries) {
		t.Fatalf("queries = %q, want %q", conn.queries, wantQueries)
	}
	if out := stdout.String(); !strings.Contains(out, `"v":"first"`) || !strings.Contains(out, `"v":"last"`) {
		t.Fatalf("output = %q, want rows before and after query error", out)
	}
}

func TestRunDefaultSubmitsWholeBatchOnce(t *testing.T) {
	batch := "SELECT 1; SELECT 2;"
	conn := &runnerConn{query: func(sql string) (db.Rows, error) {
		return newRunnerRows([]runnerResult{{rows: [][]any{{sql}}}}), nil
	}}
	opts := runnerOpts(conn, batch, output.JSONL, io.Discard, io.Discard)
	opts.continueOnError = false

	if code := run(t.Context(), opts); code != ExitOK {
		t.Fatalf("run code = %d, want %d", code, ExitOK)
	}
	if len(conn.queries) != 1 || conn.queries[0] != batch {
		t.Fatalf("queries = %q, want exact batch once", conn.queries)
	}
}

func TestRunContinueOnErrorStartsFreshTimeoutPerStatement(t *testing.T) {
	conn := &runnerConn{}
	conn.queryContext = func(ctx context.Context, sql string) (db.Rows, error) {
		if sql == "SELECT slow;" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("second statement inherited expired context: %w", err)
		}
		return newRunnerRows([]runnerResult{{rows: [][]any{{"later"}}}}), nil
	}
	var stdout, stderr bytes.Buffer
	opts := runnerOpts(conn, "SELECT slow; SELECT later;", output.JSONL, &stdout, &stderr)
	opts.timeout = 10 * time.Millisecond

	if code := run(t.Context(), opts); code != ExitQuery {
		t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitQuery, stderr.String())
	}
	if len(conn.queries) != 2 || !strings.Contains(stdout.String(), `"v":"later"`) {
		t.Fatalf("queries = %q, output = %q; later statement did not get a fresh timeout", conn.queries, stdout.String())
	}
}

func TestRunMultiResultJSONAndHTMLAreSingleDocuments(t *testing.T) {
	for _, format := range []output.Format{output.JSON, output.HTML} {
		t.Run(format.String(), func(t *testing.T) {
			conn := &runnerConn{query: func(string) (db.Rows, error) {
				return newRunnerRows([]runnerResult{
					{columns: []db.Column{{Name: "first"}}, rows: [][]any{{"one"}}},
					{columns: []db.Column{{Name: "second"}}, rows: [][]any{{"two"}}},
				}), nil
			}}
			var stdout, stderr bytes.Buffer
			opts := runnerOpts(conn, "SELECT 1; SELECT 2", format, &stdout, &stderr)
			opts.continueOnError = false
			if code := run(t.Context(), opts); code != ExitOK {
				t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitOK, stderr.String())
			}

			if format == output.JSON {
				if !json.Valid(stdout.Bytes()) {
					t.Fatalf("invalid JSON: %s", stdout.String())
				}
				var results []json.RawMessage
				if err := json.Unmarshal(stdout.Bytes(), &results); err != nil || len(results) != 2 {
					t.Fatalf("JSON results = %d, err = %v; output = %s", len(results), err, stdout.String())
				}
				return
			}
			got := stdout.String()
			if strings.Count(got, "<!doctype html>") != 1 || strings.Count(got, "<html>") != 1 || strings.Count(got, "</html>") != 1 {
				t.Fatalf("HTML is not one document: %s", got)
			}
			if strings.Count(got, "<table>") != 2 || strings.Count(got, "</table>") != 2 {
				t.Fatalf("HTML tables are unbalanced: %s", got)
			}
		})
	}
}

func TestRunMaxRowsAppliesAcrossResultSets(t *testing.T) {
	rows := newRunnerRows([]runnerResult{
		{rows: [][]any{{"one"}, {"two"}}},
		{rows: [][]any{{"three"}, {"four"}, {"five"}}},
	})
	conn := &runnerConn{query: func(string) (db.Rows, error) { return rows, nil }}
	var stdout, stderr bytes.Buffer
	opts := runnerOpts(conn, "SELECT 1; SELECT 2", output.JSONL, &stdout, &stderr)
	opts.continueOnError = false
	opts.maxRows = 3

	if code := run(t.Context(), opts); code != ExitRowCap {
		t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitRowCap, stderr.String())
	}
	if got := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; got != 3 {
		t.Fatalf("output row count = %d, want 3; output = %q", got, stdout.String())
	}
	if rows.scanCount != 3 {
		t.Fatalf("Scan calls = %d, want 3", rows.scanCount)
	}
}

func TestRunStreamingWriteFailureStopsWithoutDrainingAndRecordsHistory(t *testing.T) {
	writeErr := errors.New("sink closed")
	rowsData := make([][]any, 1000)
	for i := range rowsData {
		rowsData[i] = []any{i}
	}
	rows := newRunnerRows([]runnerResult{{rows: rowsData}})
	conn := &runnerConn{query: func(string) (db.Rows, error) { return rows, nil }}
	store := &recordingHistoryStore{fakeCLIStore: &fakeCLIStore{}}
	restore := stubOpenStore(t, store)
	defer restore()
	var stderr bytes.Buffer
	opts := runnerOpts(conn, "SELECT many", output.JSONL, failingRunnerWriter{err: writeErr}, &stderr)
	opts.recordHistory = true

	if code := run(t.Context(), opts); code != ExitQuery {
		t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitQuery, stderr.String())
	}
	if rows.scanCount != 1 {
		t.Fatalf("Scan calls = %d, want 1 after immediate stream failure", rows.scanCount)
	}
	if len(store.entries) != 1 {
		t.Fatalf("history entries = %d, want 1", len(store.entries))
	}
	entry := store.entries[0]
	if !strings.Contains(entry.Error, writeErr.Error()) || entry.RowCount != -1 {
		t.Fatalf("history entry = %+v, want truthful stream failure", entry)
	}
}

func TestRunHistoryWriteFailureDoesNotChangeSuccessfulExit(t *testing.T) {
	historyErr := errors.New("history unavailable")
	conn := &runnerConn{query: func(string) (db.Rows, error) {
		return newRunnerRows([]runnerResult{{rows: [][]any{{"ok"}}}}), nil
	}}
	store := &recordingHistoryStore{fakeCLIStore: &fakeCLIStore{}, recordErr: historyErr}
	restore := stubOpenStore(t, store)
	defer restore()
	var stderr bytes.Buffer
	opts := runnerOpts(conn, "SELECT 1", output.JSONL, io.Discard, &stderr)
	opts.recordHistory = true

	if code := run(t.Context(), opts); code != ExitOK {
		t.Fatalf("run code = %d, want %d; stderr = %q", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "history: "+historyErr.Error()) {
		t.Fatalf("stderr = %q, want history failure", stderr.String())
	}
}

func TestRunRowsCloseFailureIsReported(t *testing.T) {
	closeErr := errors.New("cursor close failed")
	conn := &runnerConn{query: func(string) (db.Rows, error) {
		rows := newRunnerRows([]runnerResult{{rows: [][]any{{"ok"}}}})
		rows.closeErr = closeErr
		return rows, nil
	}}
	var stderr bytes.Buffer
	opts := runnerOpts(conn, "SELECT 1", output.JSONL, io.Discard, &stderr)
	opts.continueOnError = false

	if code := run(t.Context(), opts); code != ExitQuery {
		t.Fatalf("run code = %d, want %d", code, ExitQuery)
	}
	if !strings.Contains(stderr.String(), "rows close: "+closeErr.Error()) {
		t.Fatalf("stderr = %q, want close failure", stderr.String())
	}
}

func runnerOpts(conn db.Conn, sql string, format output.Format, stdout, stderr io.Writer) *runOptions {
	return &runOptions{
		sql:             sql,
		format:          format,
		continueOnError: true,
		openConn: func(context.Context) (db.Conn, io.Closer, error) {
			return conn, nil, nil
		},
		out:    stdout,
		stderr: stderr,
	}
}

type runnerConn struct {
	query        func(string) (db.Rows, error)
	queryContext func(context.Context, string) (db.Rows, error)
	queries      []string
}

func (c *runnerConn) Close() error                                              { return nil }
func (c *runnerConn) Ping(context.Context) error                                { return nil }
func (c *runnerConn) Driver() string                                            { return "runner-test" }
func (c *runnerConn) Capabilities() db.Capabilities                             { return db.Capabilities{} }
func (c *runnerConn) Schema(context.Context) (*db.SchemaInfo, error)            { return nil, nil }
func (c *runnerConn) Columns(context.Context, db.TableRef) ([]db.Column, error) { return nil, nil }
func (c *runnerConn) Definition(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c *runnerConn) Explain(context.Context, string) ([][]any, error) { return nil, nil }
func (c *runnerConn) Exec(context.Context, string, ...any) error       { return nil }
func (c *runnerConn) Query(ctx context.Context, sql string) (db.Rows, error) {
	c.queries = append(c.queries, sql)
	if c.queryContext != nil {
		return c.queryContext(ctx, sql)
	}
	return c.query(sql)
}

type runnerResult struct {
	columns []db.Column
	rows    [][]any
	err     error
}

type runnerRows struct {
	results   []runnerResult
	resultIdx int
	rowIdx    int
	scanCount int
	closeErr  error
}

func newRunnerRows(results []runnerResult) *runnerRows {
	for i := range results {
		if len(results[i].columns) == 0 {
			results[i].columns = []db.Column{{Name: "v"}}
		}
	}
	return &runnerRows{results: results}
}

func (r *runnerRows) Columns() []db.Column { return r.results[r.resultIdx].columns }
func (r *runnerRows) Next() bool {
	return r.rowIdx < len(r.results[r.resultIdx].rows)
}
func (r *runnerRows) Scan() ([]any, error) {
	row := r.results[r.resultIdx].rows[r.rowIdx]
	r.rowIdx++
	r.scanCount++
	return row, nil
}
func (r *runnerRows) Err() error   { return r.results[r.resultIdx].err }
func (r *runnerRows) Close() error { return r.closeErr }
func (r *runnerRows) NextResultSet() bool {
	if r.resultIdx+1 >= len(r.results) {
		return false
	}
	r.resultIdx++
	r.rowIdx = 0
	return true
}

type failingRunnerWriter struct{ err error }

func (w failingRunnerWriter) Write([]byte) (int, error) { return 0, w.err }

type recordingHistoryStore struct {
	*fakeCLIStore
	entries   []store.HistoryEntry
	recordErr error
}

func (s *recordingHistoryStore) RecordHistory(_ context.Context, entry store.HistoryEntry) error {
	s.entries = append(s.entries, entry)
	return s.recordErr
}
