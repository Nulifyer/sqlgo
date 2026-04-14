// Package d1 registers a Cloudflare D1 HTTP client. Each Exec/Query is
// a POST to the Cloudflare REST API; D1 is SQLite-backed so the dialect,
// schema layout, and PRAGMA helpers all mirror the sqlite/libsql paths.
//
// Config mapping:
//
//	cfg.User     = Cloudflare account id
//	cfg.Database = D1 database id (the UUID-looking one)
//	cfg.Password = API token with D1:edit scope
//	cfg.Host     = optional API base override (default api.cloudflare.com)
//
// Transactions are NOT supported — every HTTP call is an isolated
// statement. The capability flag is false so the TUI disables the TX
// indicator and blocks BEGIN/COMMIT routing through a single connection.
package d1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "d1"

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
	SupportsTransactions: false,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	if cfg.User == "" {
		return nil, errors.New("d1: User required (Cloudflare account id)")
	}
	if cfg.Database == "" {
		return nil, errors.New("d1: Database required (D1 database id)")
	}
	if cfg.Password == "" {
		return nil, errors.New("d1: Password required (API token)")
	}
	base := strings.TrimRight(cfg.Host, "/")
	if base == "" {
		base = "https://api.cloudflare.com"
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	endpoint := fmt.Sprintf("%s/client/v4/accounts/%s/d1/database/%s/query",
		base, cfg.User, cfg.Database)
	c := &conn{
		endpoint: endpoint,
		token:    cfg.Password,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
	if err := c.Ping(ctx); err != nil {
		return nil, fmt.Errorf("d1 ping: %w", err)
	}
	return c, nil
}

// --- HTTP conn --------------------------------------------------------------

type conn struct {
	endpoint string
	token    string
	http     *http.Client
}

func (c *conn) Driver() string                 { return driverName }
func (c *conn) Capabilities() db.Capabilities  { return capabilities }
func (c *conn) Close() error                   { c.http.CloseIdleConnections(); return nil }
func (c *conn) Ping(ctx context.Context) error { _, _, err := c.query(ctx, "SELECT 1"); return err }

func (c *conn) Exec(ctx context.Context, sqlStr string, args ...any) error {
	if len(args) > 0 {
		return errors.New("d1: positional args unsupported; embed literals")
	}
	_, _, err := c.query(ctx, sqlStr)
	return err
}

func (c *conn) Query(ctx context.Context, sqlStr string) (db.Rows, error) {
	cols, rows, err := c.query(ctx, sqlStr)
	if err != nil {
		return nil, err
	}
	return &rowsIter{cols: cols, rows: rows}, nil
}

func (c *conn) Schema(ctx context.Context) (*db.SchemaInfo, error) {
	info := &db.SchemaInfo{Status: map[string]db.ObjectKindStatus{}}
	tables, err := c.scanTables(ctx)
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

func (c *conn) scanTables(ctx context.Context) ([]db.TableRef, error) {
	const q = `
SELECT name,
       CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view,
       CASE WHEN name LIKE 'sqlite_%' OR name LIKE '_cf_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type IN ('table','view')
ORDER BY name`
	_, rows, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]db.TableRef, 0, len(rows))
	for _, row := range rows {
		name := asString(row[0])
		kind := db.TableKindTable
		if asInt(row[1]) != 0 {
			kind = db.TableKindView
		}
		out = append(out, db.TableRef{Schema: "main", Name: name, Kind: kind, System: asInt(row[2]) != 0})
	}
	return out, nil
}

func (c *conn) scanTriggers(ctx context.Context) ([]db.TriggerRef, error) {
	const q = `SELECT name, tbl_name, sql FROM sqlite_master WHERE type = 'trigger' ORDER BY name`
	_, rows, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]db.TriggerRef, 0, len(rows))
	for _, row := range rows {
		name := asString(row[0])
		table := asString(row[1])
		body := asString(row[2])
		timing, event := parseTriggerBody(body)
		out = append(out, db.TriggerRef{Schema: "main", Table: table, Name: name, Timing: timing, Event: event})
	}
	return out, nil
}

// parseTriggerBody: same best-effort split used by the libsql adapter.
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
	_, rows, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]db.Column, 0, len(rows))
	for _, row := range rows {
		out = append(out, db.Column{Name: asString(row[0]), TypeName: asString(row[1])})
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
	_, rows, err := c.query(ctx, q)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no definition for %s %s", kind, name)
	}
	body := strings.TrimRight(asString(rows[0][0]), "\r\n\t ;")
	if body == "" {
		return "", fmt.Errorf("empty definition for %s %s", kind, name)
	}
	return fmt.Sprintf("DROP %s IF EXISTS %s;\n%s;",
		strings.ToUpper(kind), sqliteQuoteIdent(name), body), nil
}

func (c *conn) Explain(ctx context.Context, sqlStr string) ([][]any, error) {
	_, rows, err := c.query(ctx, "EXPLAIN QUERY PLAN "+sqlStr)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	return rows, nil
}

// --- Cloudflare REST wire format --------------------------------------------

type queryReq struct {
	SQL string `json:"sql"`
}

type queryResp struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  []queryResultV1 `json:"result"`
}

type queryResultV1 struct {
	// D1 returns rows as an array of JSON objects. We keep each row as
	// raw bytes and decode with json.Decoder so the original key order
	// (i.e. the SQL column order) survives — decoding straight into a
	// map loses it.
	Results []json.RawMessage `json:"results"`
	Success bool              `json:"success"`
	Meta    map[string]any    `json:"meta"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// query posts a single statement and returns columns + decoded rows. The
// meta block is discarded; callers needing rowcount can re-issue. Column
// order follows the first row's JSON key order (json.RawMessage preserves
// that in Go's encoding/json).
func (c *conn) query(ctx context.Context, sqlStr string) ([]db.Column, [][]any, error) {
	raw, err := json.Marshal(queryReq{SQL: sqlStr})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read: %w", err)
	}
	var qr queryResp
	if err := json.Unmarshal(data, &qr); err != nil {
		return nil, nil, fmt.Errorf("decode: %w: %s", err, strings.TrimSpace(string(data)))
	}
	if !qr.Success {
		if len(qr.Errors) > 0 {
			return nil, nil, fmt.Errorf("d1: %d %s", qr.Errors[0].Code, qr.Errors[0].Message)
		}
		return nil, nil, fmt.Errorf("d1: %s", strings.TrimSpace(string(data)))
	}
	if len(qr.Result) == 0 {
		return nil, nil, nil
	}
	r := qr.Result[0]
	if len(r.Results) == 0 {
		return nil, nil, nil
	}
	cols, firstValues, err := decodeRowOrdered(r.Results[0])
	if err != nil {
		return nil, nil, fmt.Errorf("row decode: %w", err)
	}
	rows := make([][]any, len(r.Results))
	rows[0] = firstValues
	for i := 1; i < len(r.Results); i++ {
		vals, err := decodeRowByOrder(r.Results[i], cols)
		if err != nil {
			return nil, nil, fmt.Errorf("row decode: %w", err)
		}
		rows[i] = vals
	}
	return cols, rows, nil
}

// decodeRowOrdered walks a row object with json.Decoder so original key
// order survives, and returns both the column list and the row's values.
func decodeRowOrdered(raw json.RawMessage) ([]db.Column, []any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	var cols []db.Column
	var vals []any
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected token %v", tok)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		cols = append(cols, db.Column{Name: key})
		vals = append(vals, decodeJSON(v))
	}
	return cols, vals, nil
}

// decodeRowByOrder projects a row onto the column order we recorded from
// row 0. D1 returns consistent keys per query, so a map-lookup is safe.
func decodeRowByOrder(raw json.RawMessage, cols []db.Column) ([]any, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make([]any, len(cols))
	for i, c := range cols {
		out[i] = decodeJSON(m[c.Name])
	}
	return out, nil
}

func decodeJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	// json decodes numbers as float64; collapse integers to int64 so the
	// grid renders 1 instead of 1.000000.
	if f, ok := v.(float64); ok {
		if f == float64(int64(f)) {
			return int64(f)
		}
	}
	return v
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
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
	rows [][]any
	i    int
}

func (r *rowsIter) Columns() []db.Column { return r.cols }
func (r *rowsIter) Next() bool            { return r.i < len(r.rows) }
func (r *rowsIter) Scan() ([]any, error) {
	if r.i >= len(r.rows) {
		return nil, errors.New("scan past end")
	}
	row := r.rows[r.i]
	r.i++
	return row, nil
}
func (r *rowsIter) Err() error          { return nil }
func (r *rowsIter) Close() error        { r.i = len(r.rows); return nil }
func (r *rowsIter) NextResultSet() bool { return false }
