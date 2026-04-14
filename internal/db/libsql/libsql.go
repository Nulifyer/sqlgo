// Package libsql registers a pure-Go client for Turso / libSQL servers
// over the hrana v3 HTTP pipeline protocol. This covers the remote Turso
// use case; we don't attempt to read libSQL's on-disk file format.
//
// cfg.Host is the server hostname (with or without scheme), cfg.Password
// is the auth token (Turso DB token). cfg.User/cfg.Database/cfg.Port are
// unused — Turso URLs identify a single database already.
//
// Transactions: hrana carries a "baton" string across requests to pin a
// stream. We thread the current baton onto each Conn so user-typed
// BEGIN/COMMIT actually hold across statements. Concurrent callers on
// the same db.Conn are serialized (baton is not concurrency-safe).
package libsql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "libsql"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:          db.SchemaDepthFlat,
	LimitSyntax:          db.LimitSyntaxLimit,
	IdentifierQuote:      '"',
	SupportsCancel:       true,
	SupportsTLS:          true,
	ExplainFormat:        db.ExplainFormatSQLiteRows,
	Dialect:              sqltok.DialectSQLite,
	SupportsTransactions: true,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	base := strings.TrimRight(cfg.Host, "/")
	if base == "" {
		return nil, errors.New("libsql: Host required (turso database URL)")
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	c := &conn{
		baseURL: base,
		token:   cfg.Password,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	if err := c.Ping(ctx); err != nil {
		return nil, fmt.Errorf("libsql ping: %w", err)
	}
	return c, nil
}

// --- HTTP conn --------------------------------------------------------------

type conn struct {
	baseURL string
	token   string
	http    *http.Client

	mu    sync.Mutex
	baton string // most recent baton returned by the server
}

func (c *conn) Driver() string                 { return driverName }
func (c *conn) Capabilities() db.Capabilities  { return capabilities }
func (c *conn) Close() error                   { c.http.CloseIdleConnections(); return nil }
func (c *conn) Ping(ctx context.Context) error { return c.exec(ctx, "SELECT 1", false) }

func (c *conn) Exec(ctx context.Context, sqlStr string, args ...any) error {
	if len(args) > 0 {
		return errors.New("libsql: positional args unsupported; embed literals")
	}
	return c.exec(ctx, sqlStr, false)
}

func (c *conn) exec(ctx context.Context, sqlStr string, wantRows bool) error {
	_, err := c.pipeline(ctx, sqlStr, wantRows)
	return err
}

func (c *conn) Query(ctx context.Context, sqlStr string) (db.Rows, error) {
	res, err := c.pipeline(ctx, sqlStr, true)
	if err != nil {
		return nil, err
	}
	cols := make([]db.Column, len(res.Cols))
	for i, col := range res.Cols {
		cols[i] = db.Column{Name: col.Name, TypeName: col.Decltype}
	}
	return &rowsIter{cols: cols, rows: res.Rows}, nil
}

func (c *conn) Schema(ctx context.Context) (*db.SchemaInfo, error) {
	info := &db.SchemaInfo{Status: map[string]db.ObjectKindStatus{}}
	tables, err := c.scanSchema(ctx)
	if err != nil {
		return nil, err
	}
	info.Tables = tables
	info.Status["routines"] = db.ObjectKindUnsupported
	triggers, err := c.scanTriggers(ctx)
	if err != nil {
		return nil, err
	}
	info.Triggers = triggers
	return info, nil
}

func (c *conn) scanSchema(ctx context.Context) ([]db.TableRef, error) {
	const q = `
SELECT name,
       CASE WHEN type = 'view' THEN 1 ELSE 0 END,
       CASE WHEN name LIKE 'sqlite_%' OR name LIKE 'libsql_%' THEN 1 ELSE 0 END
FROM sqlite_master
WHERE type IN ('table','view')
ORDER BY name`
	res, err := c.pipeline(ctx, q, true)
	if err != nil {
		return nil, err
	}
	out := make([]db.TableRef, 0, len(res.Rows))
	for _, row := range res.Rows {
		name, _ := valueAsString(row[0])
		isView, _ := valueAsInt(row[1])
		isSys, _ := valueAsInt(row[2])
		kind := db.TableKindTable
		if isView != 0 {
			kind = db.TableKindView
		}
		out = append(out, db.TableRef{Schema: "main", Name: name, Kind: kind, System: isSys != 0})
	}
	return out, nil
}

func (c *conn) scanTriggers(ctx context.Context) ([]db.TriggerRef, error) {
	const q = `
SELECT name, tbl_name, sql
FROM sqlite_master
WHERE type = 'trigger'
ORDER BY name`
	res, err := c.pipeline(ctx, q, true)
	if err != nil {
		return nil, err
	}
	out := make([]db.TriggerRef, 0, len(res.Rows))
	for _, row := range res.Rows {
		name, _ := valueAsString(row[0])
		table, _ := valueAsString(row[1])
		body, _ := valueAsString(row[2])
		timing, event := parseTriggerBody(body)
		out = append(out, db.TriggerRef{Schema: "main", Table: table, Name: name, Timing: timing, Event: event})
	}
	return out, nil
}

// parseTriggerBody digs BEFORE/AFTER/INSTEAD + INSERT/UPDATE/DELETE out
// of the CREATE TRIGGER text sqlite_master stores. Best-effort only —
// used for the explorer label, not DDL parsing.
func parseTriggerBody(sqlText string) (timing, event string) {
	upper := strings.ToUpper(sqlText)
	switch {
	case strings.Contains(upper, " BEFORE "):
		timing = "BEFORE"
	case strings.Contains(upper, " AFTER "):
		timing = "AFTER"
	case strings.Contains(upper, " INSTEAD OF "):
		timing = "INSTEAD OF"
	}
	switch {
	case strings.Contains(upper, " INSERT "):
		event = "INSERT"
	case strings.Contains(upper, " UPDATE "):
		event = "UPDATE"
	case strings.Contains(upper, " DELETE "):
		event = "DELETE"
	}
	return
}

func (c *conn) Columns(ctx context.Context, t db.TableRef) ([]db.Column, error) {
	q := fmt.Sprintf(`SELECT name, type FROM pragma_table_info(%s)`, sqliteQuoteString(t.Name))
	res, err := c.pipeline(ctx, q, true)
	if err != nil {
		return nil, err
	}
	out := make([]db.Column, 0, len(res.Rows))
	for _, row := range res.Rows {
		name, _ := valueAsString(row[0])
		typ, _ := valueAsString(row[1])
		out = append(out, db.Column{Name: name, TypeName: typ})
	}
	return out, nil
}

func (c *conn) Definition(ctx context.Context, kind, schema, name string) (string, error) {
	var typ string
	switch kind {
	case "view":
		typ = "view"
	case "trigger":
		typ = "trigger"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	q := fmt.Sprintf(`SELECT sql FROM sqlite_master WHERE type = %s AND name = %s LIMIT 1`,
		sqliteQuoteString(typ), sqliteQuoteString(name))
	res, err := c.pipeline(ctx, q, true)
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 0 {
		return "", fmt.Errorf("no definition for %s %s", kind, name)
	}
	body, _ := valueAsString(res.Rows[0][0])
	body = strings.TrimRight(body, "\r\n\t ;")
	if body == "" {
		return "", fmt.Errorf("empty definition for %s %s", kind, name)
	}
	drop := fmt.Sprintf("DROP %s IF EXISTS %s;\n", strings.ToUpper(kind), sqliteQuoteIdent(name))
	return drop + body + ";", nil
}

func (c *conn) Explain(ctx context.Context, sqlStr string) ([][]any, error) {
	res, err := c.pipeline(ctx, "EXPLAIN QUERY PLAN "+sqlStr, true)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	out := make([][]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		r := make([]any, len(row))
		for i, v := range row {
			r[i] = valueToAny(v)
		}
		out = append(out, r)
	}
	return out, nil
}

// --- hrana pipeline wire format ---------------------------------------------

type hranaColumn struct {
	Name     string `json:"name"`
	Decltype string `json:"decltype,omitempty"`
}

type hranaValue struct {
	Type   string      `json:"type"`
	Value  interface{} `json:"value,omitempty"`
	Base64 string      `json:"base64,omitempty"`
}

type hranaStmtResult struct {
	Cols []hranaColumn  `json:"cols"`
	Rows [][]hranaValue `json:"rows"`
}

type hranaRequest struct {
	Type string     `json:"type"`
	Stmt *hranaStmt `json:"stmt,omitempty"`
}

type hranaStmt struct {
	SQL      string `json:"sql"`
	WantRows bool   `json:"want_rows"`
}

type hranaPipelineReq struct {
	Baton    string         `json:"baton,omitempty"`
	Requests []hranaRequest `json:"requests"`
}

type hranaPipelineResp struct {
	Baton   string             `json:"baton"`
	BaseURL string             `json:"base_url"`
	Results []hranaResultEntry `json:"results"`
}

type hranaResultEntry struct {
	Type     string           `json:"type"` // "ok" or "error"
	Response *hranaResponseOK `json:"response,omitempty"`
	Error    *hranaError      `json:"error,omitempty"`
}

type hranaResponseOK struct {
	Type   string           `json:"type"`
	Result *hranaStmtResult `json:"result,omitempty"`
}

type hranaError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func (e *hranaError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("libsql: %s (%s)", e.Message, e.Code)
	}
	return "libsql: " + e.Message
}

// pipeline sends one `execute` request (optionally within the current
// baton stream) and returns the statement result. Baton is updated from
// the response so the next call joins the same stream.
func (c *conn) pipeline(ctx context.Context, sqlStr string, wantRows bool) (*hranaStmtResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	body := hranaPipelineReq{
		Baton: c.baton,
		Requests: []hranaRequest{
			{Type: "execute", Stmt: &hranaStmt{SQL: sqlStr, WantRows: wantRows}},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/pipeline", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("libsql: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var pr hranaPipelineResp
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("decode: %w: %s", err, string(data))
	}
	c.baton = pr.Baton
	if len(pr.Results) == 0 {
		return nil, errors.New("libsql: empty response")
	}
	r := pr.Results[0]
	if r.Type == "error" || r.Error != nil {
		if r.Error != nil {
			return nil, r.Error
		}
		return nil, errors.New("libsql: server error")
	}
	if r.Response == nil || r.Response.Result == nil {
		return &hranaStmtResult{}, nil
	}
	return r.Response.Result, nil
}

// --- value helpers ----------------------------------------------------------

// UnmarshalJSON handles hrana's tagged-value encoding. Integers arrive as
// JSON strings to survive 53-bit float precision; floats/bools/text pass
// through.
func (v *hranaValue) UnmarshalJSON(data []byte) error {
	type raw struct {
		Type   string          `json:"type"`
		Value  json.RawMessage `json:"value"`
		Base64 string          `json:"base64"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	v.Type = r.Type
	v.Base64 = r.Base64
	if len(r.Value) == 0 {
		return nil
	}
	switch r.Type {
	case "integer":
		s := strings.Trim(string(r.Value), `"`)
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("parse integer %q: %w", s, err)
		}
		v.Value = n
	case "float":
		var f float64
		if err := json.Unmarshal(r.Value, &f); err != nil {
			return err
		}
		v.Value = f
	case "text":
		var s string
		if err := json.Unmarshal(r.Value, &s); err != nil {
			return err
		}
		v.Value = s
	default:
		var any interface{}
		if err := json.Unmarshal(r.Value, &any); err != nil {
			return err
		}
		v.Value = any
	}
	return nil
}

func valueToAny(v hranaValue) any {
	switch v.Type {
	case "null":
		return nil
	case "blob":
		if v.Base64 == "" {
			return []byte(nil)
		}
		b, err := base64.StdEncoding.DecodeString(v.Base64)
		if err != nil {
			return v.Base64
		}
		return b
	default:
		return v.Value
	}
}

func valueAsString(v hranaValue) (string, bool) {
	if s, ok := v.Value.(string); ok {
		return s, true
	}
	return "", false
}

func valueAsInt(v hranaValue) (int64, bool) {
	if n, ok := v.Value.(int64); ok {
		return n, true
	}
	return 0, false
}

func sqliteQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func sqliteQuoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// --- rows iterator ----------------------------------------------------------

type rowsIter struct {
	cols []db.Column
	rows [][]hranaValue
	i    int
	err  error
}

func (r *rowsIter) Columns() []db.Column { return r.cols }
func (r *rowsIter) Next() bool {
	if r.err != nil {
		return false
	}
	return r.i < len(r.rows)
}
func (r *rowsIter) Scan() ([]any, error) {
	if r.i >= len(r.rows) {
		return nil, errors.New("scan past end")
	}
	row := r.rows[r.i]
	r.i++
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = valueToAny(v)
	}
	return out, nil
}
func (r *rowsIter) Err() error          { return r.err }
func (r *rowsIter) Close() error        { r.i = len(r.rows); return nil }
func (r *rowsIter) NextResultSet() bool { return false }

// unused — keeps sql import alive for errors.Is(err, sql.ErrNoRows)
// compatibility with the shared definition signature used elsewhere.
var _ = sql.ErrNoRows
