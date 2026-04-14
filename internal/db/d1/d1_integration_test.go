//go:build integration

package d1

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/dbtest"
)

// TestIntegrationD1 exercises the D1 HTTP adapter against an
// in-process fake Cloudflare REST server. D1 has no self-hostable
// image, so this verifies the wire format (POST /query, ordered-row
// JSON, integer collapse) end-to-end against a real SQLite engine.
func TestIntegrationD1(t *testing.T) {
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()

	srv := httptest.NewServer(newFakeD1Handler(t, sqlDB))
	defer srv.Close()

	cfg := db.Config{
		Host:     srv.URL,
		User:     "fake-account",
		Database: "fake-db-id",
		Password: "fake-token",
	}
	d, err := db.Get("d1")
	if err != nil {
		t.Fatalf("db.Get d1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := d.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open d1 (fake): %v", err)
	}
	defer conn.Close()

	dbtest.ExerciseDriver(t, conn, "main",
		`CREATE TABLE sqlgo_it_d1 (id INTEGER, label TEXT)`,
		"sqlgo_it_d1",
	)
}

// fakeD1Handler replies to the Cloudflare D1 query endpoint. One
// SQLite connection is shared across requests under a mutex so
// CREATE/INSERT/SELECT see each other just like real D1 does.
func newFakeD1Handler(t *testing.T, sqlDB *sql.DB) http.Handler {
	var mu sync.Mutex
	expectedPath := "/client/v4/accounts/fake-account/d1/database/fake-db-id/query"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fake-token" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeCFErr(w, 1000, err.Error())
			return
		}
		var req struct {
			SQL string `json:"sql"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeCFErr(w, 1001, "bad request body")
			return
		}

		mu.Lock()
		defer mu.Unlock()
		cols, rows, execErr := runSQL(r.Context(), sqlDB, req.SQL)
		if execErr != nil {
			writeCFErr(w, 7500, execErr.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, encodeCFResult(cols, rows))
	})
}

// runSQL decides Query vs Exec from the SQL verb. D1 itself exposes
// a unified endpoint but we need database/sql's split API under it.
func runSQL(ctx context.Context, sqlDB *sql.DB, sqlStr string) ([]string, [][]any, error) {
	trim := strings.TrimSpace(sqlStr)
	verb := strings.ToUpper(trim)
	if i := strings.IndexAny(verb, " \t\r\n"); i > 0 {
		verb = verb[:i]
	}
	switch verb {
	case "SELECT", "PRAGMA", "EXPLAIN", "WITH":
		rows, err := sqlDB.QueryContext(ctx, sqlStr)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, nil, err
		}
		out := [][]any{}
		for rows.Next() {
			scan := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range scan {
				ptrs[i] = &scan[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, nil, err
			}
			out = append(out, scan)
		}
		return cols, out, rows.Err()
	default:
		if _, err := sqlDB.ExecContext(ctx, sqlStr); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
}

// encodeCFResult emits the exact Cloudflare shape the d1 adapter
// parses. Rows are serialized with ordered keys (json.RawMessage
// decoding in the adapter preserves that order).
func encodeCFResult(cols []string, rows [][]any) string {
	var b strings.Builder
	b.WriteString(`{"success":true,"errors":[],"result":[{"success":true,"meta":{},"results":[`)
	for i, row := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		for j, c := range cols {
			if j > 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(c)
			b.Write(k)
			b.WriteByte(':')
			b.WriteString(jsonValue(row[j]))
		}
		b.WriteByte('}')
	}
	b.WriteString(`]}]}`)
	return b.String()
}

func jsonValue(v any) string {
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case []byte:
		// sqlite returns TEXT as []byte for some column types.
		s, _ := json.Marshal(string(x))
		return string(s)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		s, _ := json.Marshal(x)
		return string(s)
	default:
		s, err := json.Marshal(x)
		if err != nil {
			return "null"
		}
		return string(s)
	}
}

func writeCFErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":false,"errors":[{"code":%d,"message":%q}],"result":[]}`, code, msg)
}
