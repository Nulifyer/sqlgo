// Package bigquery registers a Google BigQuery client. No good
// database/sql driver exists for BigQuery (the handful of community
// attempts all require paid proprietary ODBC bridges), so this adapter
// implements db.Conn directly on top of cloud.google.com/go/bigquery
// like the D1 and Athena adapters.
//
// Config mapping:
//
//	cfg.Options["project"]          -> GCP project id (required)
//	cfg.Database                    -> default dataset id (optional)
//	cfg.Options["dataset"]          -> default dataset id (alias for cfg.Database)
//	cfg.Options["credentials"]      -> path to service-account key JSON
//	cfg.Options["credentials_json"] -> inline service-account key JSON
//	cfg.Options["access_token"]     -> short-lived OAuth access token (StaticTokenSource)
//	cfg.Options["endpoint"]         -> API endpoint override (emulator)
//	cfg.Options["location"]         -> query location (US, EU, asia-northeast1 ...)
//	cfg.Options["disable_auth"]     -> skip ADC; required for goccy emulator
//	cfg.Host / cfg.Port             -> emulator endpoint builder
//
// access_token is the pre-fetched path for short-lived Google OAuth
// bearer tokens (`gcloud auth print-access-token`, Workload Identity
// Federation exchanges, on-demand OIDC exchanges). It wraps the raw
// value in oauth2.StaticTokenSource -- there is no refresh, so the
// token needs to outlive the session or the caller reconnects. When
// set, ADC + credentials / credentials_json are ignored by the BigQuery
// client. Refresh loops belong to the caller, not sqlgo.
//
// SchemaDepth is Schemas because a BigQuery project contains multiple
// datasets and each dataset is an independent namespace of tables+views.
// Datasets map to the "schema" tier in the explorer. SupportsTransactions
// is false: BigQuery's multi-statement transactions only live inside a
// single script, which sqlgo's one-statement-per-Exec model does not use.
package bigquery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"golang.org/x/oauth2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "bigquery"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:          db.SchemaDepthSchemas,
	LimitSyntax:          db.LimitSyntaxLimit,
	IdentifierQuote:      '`',
	SupportsCancel:       true,
	SupportsTLS:          true,
	ExplainFormat:        db.ExplainFormatNone,
	Dialect:              sqltok.DialectBigQuery,
	SupportsTransactions: false,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	project := firstNonEmpty(cfg.Options["project"], cfg.Options["project_id"])
	if project == "" {
		return nil, errors.New("bigquery: Options[\"project\"] required")
	}
	defaultDataset := firstNonEmpty(cfg.Database, cfg.Options["dataset"])
	location := cfg.Options["location"]

	opts, err := buildClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	client, err := bigquery.NewClient(ctx, project, opts...)
	if err != nil {
		return nil, fmt.Errorf("bigquery: client: %w", err)
	}
	if location != "" {
		client.Location = location
	}

	c := &conn{
		client:         client,
		project:        project,
		defaultDataset: defaultDataset,
		location:       location,
	}
	if err := c.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("bigquery ping: %w", err)
	}
	return c, nil
}

// buildClientOptions turns sqlgo's Options map into the option.ClientOption
// slice the bigquery client wants. Emulator mode is inferred from Host+Port
// (or an explicit endpoint) and implies WithoutAuthentication unless the
// caller has already supplied credentials.
func buildClientOptions(cfg db.Config) ([]option.ClientOption, error) {
	var opts []option.ClientOption

	endpoint := strings.TrimSpace(cfg.Options["endpoint"])
	if endpoint == "" && strings.TrimSpace(cfg.Host) != "" {
		host := strings.TrimSpace(cfg.Host)
		if !strings.Contains(host, "://") {
			host = "http://" + host
		}
		if cfg.Port > 0 {
			host = host + ":" + strconv.Itoa(cfg.Port)
		}
		endpoint = host
	}
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}

	accessToken := strings.TrimSpace(cfg.Options["access_token"])

	disableAuth := boolish(cfg.Options["disable_auth"])
	// Point at the emulator -> skip auth unless user explicitly provided creds.
	// Keeps the emulator path zero-config.
	if !disableAuth && endpoint != "" && accessToken == "" &&
		cfg.Options["credentials"] == "" && cfg.Options["credentials_json"] == "" {
		disableAuth = true
	}

	if disableAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	if accessToken != "" {
		// StaticTokenSource hands back the same bearer indefinitely --
		// there is no refresh. Good enough for short sessions; callers
		// on long-lived connections must reconnect once the token expires.
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
		opts = append(opts, option.WithTokenSource(ts))
	}
	if v := strings.TrimSpace(cfg.Options["credentials"]); v != "" {
		opts = append(opts, option.WithCredentialsFile(v))
	}
	if v := strings.TrimSpace(cfg.Options["credentials_json"]); v != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(v)))
	}
	return opts, nil
}

// --- conn -------------------------------------------------------------------

type conn struct {
	client         *bigquery.Client
	project        string
	defaultDataset string
	location       string
}

func (c *conn) Driver() string                { return driverName }
func (c *conn) Capabilities() db.Capabilities { return capabilities }

func (c *conn) Close() error {
	if c.client == nil {
		return nil
	}
	err := c.client.Close()
	c.client = nil
	return err
}

func (c *conn) Ping(ctx context.Context) error {
	// Cheapest round-trip that actually hits the REST API. A dry-run of
	// SELECT 1 validates both transport and the project id without
	// spinning up a real job.
	q := c.client.Query("SELECT 1")
	q.DryRun = true
	if c.location != "" {
		q.Location = c.location
	}
	_, err := q.Run(ctx)
	return err
}

func (c *conn) Exec(ctx context.Context, sqlStr string, args ...any) error {
	if len(args) > 0 {
		return errors.New("bigquery: positional args unsupported; embed literals")
	}
	q := c.buildQuery(sqlStr)
	job, err := q.Run(ctx)
	if err != nil {
		return err
	}
	status, err := job.Wait(ctx)
	if err != nil {
		return err
	}
	if err := status.Err(); err != nil {
		return err
	}
	return nil
}

func (c *conn) Query(ctx context.Context, sqlStr string) (db.Rows, error) {
	q := c.buildQuery(sqlStr)
	it, err := q.Read(ctx)
	if err != nil {
		return nil, err
	}
	return newRowsIter(it), nil
}

func (c *conn) buildQuery(sqlStr string) *bigquery.Query {
	q := c.client.Query(sqlStr)
	if c.location != "" {
		q.Location = c.location
	}
	if c.defaultDataset != "" {
		q.DefaultProjectID = c.project
		q.DefaultDatasetID = c.defaultDataset
	}
	return q
}

func (c *conn) Schema(ctx context.Context) (*db.SchemaInfo, error) {
	info := &db.SchemaInfo{Status: map[string]db.ObjectKindStatus{}}

	dsIt := c.client.Datasets(ctx)
	for {
		dsRef, err := dsIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list datasets: %w", err)
		}
		// If the caller pinned a single dataset, hide the others so the
		// explorer stays focused on the active namespace.
		if c.defaultDataset != "" && dsRef.DatasetID != c.defaultDataset {
			continue
		}
		tables, err := c.listTables(ctx, dsRef)
		if err != nil {
			return nil, err
		}
		info.Tables = append(info.Tables, tables...)
	}

	// BigQuery has stored procedures and table functions in INFORMATION_
	// SCHEMA.ROUTINES but no first-class trigger concept. Leave both off
	// the critical path for MVP.
	info.Status["routines"] = db.ObjectKindUnsupported
	info.Status["triggers"] = db.ObjectKindUnsupported
	return info, nil
}

func (c *conn) listTables(ctx context.Context, dsRef *bigquery.Dataset) ([]db.TableRef, error) {
	var out []db.TableRef
	tIt := dsRef.Tables(ctx)
	for {
		t, err := tIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list tables for %s: %w", dsRef.DatasetID, err)
		}
		out = append(out, db.TableRef{
			Schema: dsRef.DatasetID,
			Name:   t.TableID,
			Kind:   db.TableKindTable,
		})
	}
	return out, nil
}

func (c *conn) Columns(ctx context.Context, t db.TableRef) ([]db.Column, error) {
	schema := t.Schema
	if schema == "" {
		schema = c.defaultDataset
	}
	if schema == "" {
		return nil, errors.New("bigquery: dataset (schema) required to list columns")
	}
	md, err := c.client.Dataset(schema).Table(t.Name).Metadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("bigquery: metadata %s.%s: %w", schema, t.Name, err)
	}
	out := make([]db.Column, 0, len(md.Schema))
	for _, f := range md.Schema {
		out = append(out, db.Column{
			Name:     f.Name,
			TypeName: fieldTypeName(f),
		})
	}
	return out, nil
}

// fieldTypeName renders a BigQuery FieldSchema's type in the form users
// see in the web console: STRING, INT64, RECORD (with nested spelled out
// at the leaf level), plus REPEATED/ARRAY markers.
func fieldTypeName(f *bigquery.FieldSchema) string {
	name := string(f.Type)
	if f.Repeated {
		return "ARRAY<" + name + ">"
	}
	return name
}

func (c *conn) Definition(ctx context.Context, kind, schema, name string) (string, error) {
	if schema == "" {
		schema = c.defaultDataset
	}
	if schema == "" {
		return "", errors.New("bigquery: dataset (schema) required for definition lookup")
	}
	md, err := c.client.Dataset(schema).Table(name).Metadata(ctx)
	if err != nil {
		return "", fmt.Errorf("bigquery: metadata %s.%s: %w", schema, name, err)
	}
	switch kind {
	case "view":
		body := strings.TrimSpace(md.ViewQuery)
		if body == "" {
			return "", fmt.Errorf("bigquery: no view body for %s.%s", schema, name)
		}
		qualified := "`" + c.project + "`.`" + schema + "`.`" + name + "`"
		return fmt.Sprintf("CREATE OR REPLACE VIEW %s AS\n%s;", qualified,
			strings.TrimRight(body, "\r\n\t ;")), nil
	default:
		return "", db.ErrDefinitionUnsupported
	}
}

func (c *conn) Explain(ctx context.Context, sqlStr string) ([][]any, error) {
	return nil, db.ErrExplainUnsupported
}

// --- rowsIter ---------------------------------------------------------------

// rowsIter wraps a bigquery.RowIterator. BigQuery populates the row
// schema only after the first Next() returns, so we prime a single row
// on construction to make Columns() immediately available -- callers
// that only look at schema (autocomplete lookups, dry-runs) need it to
// be ready before they touch rows.
type rowsIter struct {
	it     *bigquery.RowIterator
	cols   []db.Column
	primed bool
	buffer []bigquery.Value
	done   bool
	err    error
}

func newRowsIter(it *bigquery.RowIterator) *rowsIter {
	r := &rowsIter{it: it}
	r.prime()
	return r
}

func (r *rowsIter) prime() {
	var row []bigquery.Value
	err := r.it.Next(&row)
	switch {
	case err == iterator.Done:
		r.done = true
	case err != nil:
		r.err = err
		r.done = true
	default:
		r.primed = true
		r.buffer = row
	}
	r.cols = schemaToColumns(r.it.Schema)
}

func schemaToColumns(schema bigquery.Schema) []db.Column {
	out := make([]db.Column, 0, len(schema))
	for _, f := range schema {
		out = append(out, db.Column{Name: f.Name, TypeName: fieldTypeName(f)})
	}
	return out
}

func (r *rowsIter) Columns() []db.Column { return r.cols }

func (r *rowsIter) Next() bool {
	if r.done && !r.primed {
		return false
	}
	if r.primed {
		return true
	}
	var row []bigquery.Value
	err := r.it.Next(&row)
	switch {
	case err == iterator.Done:
		r.done = true
		return false
	case err != nil:
		r.err = err
		r.done = true
		return false
	}
	r.buffer = row
	r.primed = true
	return true
}

func (r *rowsIter) Scan() ([]any, error) {
	if !r.primed {
		return nil, errors.New("bigquery: scan without next")
	}
	row := r.buffer
	r.buffer = nil
	r.primed = false
	out := make([]any, len(row))
	for i, v := range row {
		out[i] = convertValue(v)
	}
	return out, nil
}

func (r *rowsIter) Err() error          { return r.err }
func (r *rowsIter) Close() error        { r.done = true; r.primed = false; r.buffer = nil; return nil }
func (r *rowsIter) NextResultSet() bool { return false }

// convertValue turns BigQuery's []bigquery.Value scan result -- which is
// []interface{} with a few BQ-specific types nested inside -- into plain
// Go values the grid renderer already knows how to format.
//
// ARRAY -> []any, STRUCT/RECORD -> map[string]any (so nested records
// round-trip cleanly through the JSON formatter). time.Time, civil.Date,
// civil.Time, civil.DateTime, big.Rat and []byte all fall through to the
// default case unchanged.
func convertValue(v bigquery.Value) any {
	switch vv := v.(type) {
	case nil:
		return nil
	case []bigquery.Value:
		out := make([]any, len(vv))
		for i, x := range vv {
			out[i] = convertValue(x)
		}
		return out
	case map[string]bigquery.Value:
		out := make(map[string]any, len(vv))
		for k, x := range vv {
			out[k] = convertValue(x)
		}
		return out
	case time.Time:
		return vv
	default:
		return v
	}
}

// --- helpers ----------------------------------------------------------------

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// boolish accepts the usual truthy spellings. Matches the convention the
// other adapters use for checkbox-style option fields.
func boolish(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	}
	return false
}
