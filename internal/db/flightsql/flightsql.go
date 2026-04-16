// Package flightsql registers an Apache Arrow Flight SQL client. The
// Flight SQL protocol runs over gRPC and is not backed by database/sql,
// so this adapter implements db.Conn directly -- same pattern as the
// bigquery, d1, and libsql adapters.
//
// Config mapping:
//
//	cfg.Host + cfg.Port  -> gRPC target (host:port)
//	cfg.User             -> basic-auth username (optional)
//	cfg.Password         -> basic-auth password (optional)
//	cfg.Database         -> unused by the wire protocol itself
//	cfg.Options["secure"]              -> dial with TLS
//	cfg.Options["tls_ca_file"]         -> CA bundle path
//	cfg.Options["tls_cert_file"]       -> client cert path (mTLS)
//	cfg.Options["tls_key_file"]        -> client key path  (mTLS)
//	cfg.Options["tls_server_name"]     -> override SNI
//	cfg.Options["tls_insecure_skip_verify"] -> skip server cert check
//	cfg.Options["path"]                -> reserved for future use
//
// SchemaDepth is Schemas because Flight SQL's GetTables returns
// (catalog, db_schema, table) triples. SupportsTransactions is false:
// Flight SQL has optional transaction RPCs but most servers (DuckDB,
// SQLite backends) do not implement them, and sqlgo's model is
// user-driven BEGIN/COMMIT anyway.
package flightsql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/flight/flightsql"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "flightsql"

func init() {
	db.Register(driver{})
}

type driver struct{}

func (driver) Name() string { return driverName }

var capabilities = db.Capabilities{
	SchemaDepth:          db.SchemaDepthSchemas,
	LimitSyntax:          db.LimitSyntaxLimit,
	IdentifierQuote:      '"',
	SupportsCancel:       true,
	SupportsTLS:          true,
	ExplainFormat:        db.ExplainFormatNone,
	SupportsTransactions: false,
	Dialect:              sqltok.DialectAll,
}

func (driver) Capabilities() db.Capabilities { return capabilities }

func (driver) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	dialOpts, err := buildDialOpts(cfg.Options)
	if err != nil {
		return nil, fmt.Errorf("flightsql: tls: %w", err)
	}

	client, err := flightsql.NewClientCtx(ctx, addr, nil, nil, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("flightsql: dial %s: %w", addr, err)
	}

	c := &conn{client: client}

	if cfg.User != "" {
		authCtx, err := client.Client.AuthenticateBasicToken(ctx, cfg.User, cfg.Password)
		if err != nil {
			client.Client.Close()
			return nil, fmt.Errorf("flightsql: auth: %w", err)
		}
		if md, ok := metadata.FromOutgoingContext(authCtx); ok {
			c.md = md
		}
	}

	if err := c.Ping(ctx); err != nil {
		client.Client.Close()
		return nil, fmt.Errorf("flightsql: ping: %w", err)
	}
	return c, nil
}

func buildDialOpts(opts map[string]string) ([]grpc.DialOption, error) {
	if boolish(opts["secure"]) {
		tc, err := tlsConfigFromOptions(opts)
		if err != nil {
			return nil, err
		}
		return []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tc))}, nil
	}
	return []grpc.DialOption{grpc.WithTransportCredentials(grpcinsecure.NewCredentials())}, nil
}

func tlsConfigFromOptions(opts map[string]string) (*tls.Config, error) {
	tc := &tls.Config{
		ServerName:         opts["tls_server_name"],
		InsecureSkipVerify: boolish(opts["tls_insecure_skip_verify"]),
	}
	if caPath := opts["tls_ca_file"]; caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read CA %s: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certs parsed from %s", caPath)
		}
		tc.RootCAs = pool
	}
	certPath, keyPath := opts["tls_cert_file"], opts["tls_key_file"]
	if certPath != "" && keyPath != "" {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("client cert: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// --- conn --------------------------------------------------------------------

type conn struct {
	client *flightsql.Client
	md     metadata.MD
}

func (c *conn) Driver() string                { return driverName }
func (c *conn) Capabilities() db.Capabilities { return capabilities }

func (c *conn) callCtx(ctx context.Context) context.Context {
	if c.md != nil {
		return metadata.NewOutgoingContext(ctx, c.md)
	}
	return ctx
}

func (c *conn) Close() error {
	if c.client == nil {
		return nil
	}
	err := c.client.Client.Close()
	c.client = nil
	return err
}

func (c *conn) Ping(ctx context.Context) error {
	_, err := c.client.Execute(c.callCtx(ctx), "SELECT 1")
	return err
}

func (c *conn) Exec(ctx context.Context, sql string, args ...any) error {
	if len(args) > 0 {
		return errors.New("flightsql: positional args unsupported; embed literals")
	}
	_, err := c.client.ExecuteUpdate(c.callCtx(ctx), sql)
	return err
}

func (c *conn) Query(ctx context.Context, sql string) (db.Rows, error) {
	rCtx := c.callCtx(ctx)
	info, err := c.client.Execute(rCtx, sql)
	if err != nil {
		return nil, err
	}
	if len(info.Endpoint) == 0 {
		return nil, errors.New("flightsql: no endpoints in FlightInfo")
	}
	reader, err := c.client.DoGet(rCtx, info.Endpoint[0].Ticket)
	if err != nil {
		return nil, err
	}
	return newRowsIter(reader), nil
}

func (c *conn) Schema(ctx context.Context) (*db.SchemaInfo, error) {
	rCtx := c.callCtx(ctx)
	info, err := c.client.GetTables(rCtx, &flightsql.GetTablesOpts{
		IncludeSchema: false,
	})
	if err != nil {
		return nil, fmt.Errorf("flightsql: GetTables: %w", err)
	}
	if len(info.Endpoint) == 0 {
		return &db.SchemaInfo{
			Status: map[string]db.ObjectKindStatus{
				"routines": db.ObjectKindUnsupported,
				"triggers": db.ObjectKindUnsupported,
			},
		}, nil
	}
	reader, err := c.client.DoGet(rCtx, info.Endpoint[0].Ticket)
	if err != nil {
		return nil, fmt.Errorf("flightsql: DoGet tables: %w", err)
	}
	defer reader.Release()

	schema := reader.Schema()
	idxCatalog := fieldIndex(schema, "catalog_name")
	idxSchema := fieldIndex(schema, "db_schema_name")
	idxTable := fieldIndex(schema, "table_name")
	idxType := fieldIndex(schema, "table_type")

	si := &db.SchemaInfo{Status: map[string]db.ObjectKindStatus{
		"routines": db.ObjectKindUnsupported,
		"triggers": db.ObjectKindUnsupported,
	}}

	for reader.Next() {
		rec := reader.Record()
		for i := 0; i < int(rec.NumRows()); i++ {
			ref := db.TableRef{}
			if idxCatalog >= 0 {
				ref.Catalog = cellString(rec.Column(idxCatalog), i)
			}
			if idxSchema >= 0 {
				ref.Schema = cellString(rec.Column(idxSchema), i)
			}
			if idxTable >= 0 {
				ref.Name = cellString(rec.Column(idxTable), i)
			}
			if idxType >= 0 {
				tt := strings.ToUpper(cellString(rec.Column(idxType), i))
				switch tt {
				case "TABLE":
					ref.Kind = db.TableKindTable
				case "VIEW":
					ref.Kind = db.TableKindView
				default:
					continue
				}
			}
			si.Tables = append(si.Tables, ref)
		}
	}
	if err := reader.Err(); err != nil {
		return nil, fmt.Errorf("flightsql: read tables: %w", err)
	}
	return si, nil
}

func (c *conn) Columns(ctx context.Context, t db.TableRef) ([]db.Column, error) {
	rCtx := c.callCtx(ctx)
	q := "SELECT * FROM " + quoteIdent(t.Name) + " LIMIT 0"

	if sc, err := c.columnsViaGetSchema(rCtx, q); err == nil {
		return sc, nil
	}
	return c.columnsViaExecute(rCtx, q)
}

func (c *conn) columnsViaGetSchema(ctx context.Context, q string) ([]db.Column, error) {
	sr, err := c.client.GetExecuteSchema(ctx, q)
	if err != nil {
		return nil, err
	}
	sc, err := flight.DeserializeSchema(sr.Schema, memory.DefaultAllocator)
	if err != nil {
		return nil, err
	}
	return fieldsToColumns(sc.Fields()), nil
}

func (c *conn) columnsViaExecute(ctx context.Context, q string) ([]db.Column, error) {
	info, err := c.client.Execute(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("flightsql: columns execute: %w", err)
	}
	if len(info.Endpoint) == 0 {
		return nil, errors.New("flightsql: no endpoints for column query")
	}
	reader, err := c.client.DoGet(ctx, info.Endpoint[0].Ticket)
	if err != nil {
		return nil, fmt.Errorf("flightsql: columns doget: %w", err)
	}
	defer reader.Release()
	return fieldsToColumns(reader.Schema().Fields()), nil
}

func fieldsToColumns(fields []arrow.Field) []db.Column {
	out := make([]db.Column, len(fields))
	for i, f := range fields {
		out[i] = db.Column{Name: f.Name, TypeName: f.Type.String()}
	}
	return out
}

func (c *conn) Definition(_ context.Context, _, _, _ string) (string, error) {
	return "", db.ErrDefinitionUnsupported
}

func (c *conn) Explain(_ context.Context, _ string) ([][]any, error) {
	return nil, db.ErrExplainUnsupported
}

// --- rowsIter ----------------------------------------------------------------

type rowsIter struct {
	reader *flight.Reader
	rec    arrow.Record
	cursor int64
	rows   int64
	cols   []db.Column
	done   bool
	err    error
}

func newRowsIter(r *flight.Reader) *rowsIter {
	ri := &rowsIter{reader: r}
	ri.cols = fieldsToColumns(r.Schema().Fields())
	ri.advance()
	return ri
}

func (r *rowsIter) advance() {
	if !r.reader.Next() {
		r.done = true
		r.err = r.reader.Err()
		return
	}
	r.rec = r.reader.Record()
	r.rows = r.rec.NumRows()
	r.cursor = 0
}

func (r *rowsIter) Columns() []db.Column { return r.cols }

func (r *rowsIter) Next() bool {
	if r.done {
		return false
	}
	if r.rec != nil && r.cursor < r.rows {
		return true
	}
	r.advance()
	return !r.done && r.cursor < r.rows
}

func (r *rowsIter) Scan() ([]any, error) {
	if r.done || r.rec == nil || r.cursor >= r.rows {
		return nil, errors.New("flightsql: scan without next")
	}
	nc := int(r.rec.NumCols())
	out := make([]any, nc)
	idx := int(r.cursor)
	for i := 0; i < nc; i++ {
		col := r.rec.Column(i)
		if col.IsNull(idx) {
			out[i] = nil
		} else {
			out[i] = col.GetOneForMarshal(idx)
		}
	}
	r.cursor++
	return out, nil
}

func (r *rowsIter) Err() error { return r.err }

func (r *rowsIter) Close() error {
	r.done = true
	r.rec = nil
	if r.reader != nil {
		r.reader.Release()
		r.reader = nil
	}
	return nil
}

func (r *rowsIter) NextResultSet() bool { return false }

// --- helpers -----------------------------------------------------------------

func fieldIndex(sc *arrow.Schema, name string) int {
	indices := sc.FieldIndices(name)
	if len(indices) == 0 {
		return -1
	}
	return indices[0]
}

func cellString(col arrow.Array, i int) string {
	if col.IsNull(i) {
		return ""
	}
	v := col.GetOneForMarshal(i)
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func boolish(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	}
	return false
}
