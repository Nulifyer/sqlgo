package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// SQLOptions holds engine-specific knobs for the shared sqlConn
// wrapper. Adapters build one and pass it to OpenSQL.
//
// SchemaQuery must return (schema, name, is_view int, is_system int).
// Flat-schema engines synthesize a placeholder schema like "main".
// is_system flags engine-internal catalogs (pg_catalog, sys, etc.)
// so the explorer can group them under a Sys header.
//
// ColumnsQuery takes (schema, table) positional args and returns
// (col_name, type_name). Placeholder style varies per driver.
//
// ColumnsBuilder is the escape hatch for engines that can't take
// bind values for the column lookup (sqlite PRAGMA). Takes
// precedence over ColumnsQuery.
type SQLOptions struct {
	DriverName     string
	Capabilities   Capabilities
	SchemaQuery    string
	ColumnsQuery   string
	ColumnsBuilder func(t TableRef) (string, []any)

	// RoutinesQuery, if set, must return (schema, name, kind, language, is_system)
	// where kind is 'P' (procedure), 'F' (function), or 'A' (aggregate).
	RoutinesQuery string
	// TriggersQuery, if set, must return (schema, table, name, timing, event, is_system).
	TriggersQuery string

	// IsPermissionDenied classifies a driver error as "user lacks rights"
	// (MSSQL 229/297, Postgres SQLSTATE 42501, MySQL 1142/1044). When set
	// and it returns true, the Schema loader marks that object kind as
	// denied and continues instead of failing the whole refresh.
	IsPermissionDenied func(error) bool

	// DefinitionFetcher returns runnable DDL for a single object. kind is
	// one of "view", "procedure", "function", "trigger". Adapters return
	// ErrDefinitionUnsupported for kinds they can't satisfy. Nil means the
	// driver doesn't implement Definition at all.
	DefinitionFetcher func(ctx context.Context, db *sql.DB, kind, schema, name string) (string, error)

	// ExplainRunner is an optional override that executes the engine's
	// EXPLAIN flow end-to-end and returns the raw plan rows. When nil,
	// sqlConn.Explain falls back to the default wrap-and-query flow
	// driven by Capabilities.ExplainFormat. MSSQL supplies its own so it
	// can pin a *sql.Conn across `SET SHOWPLAN_XML ON` + the target
	// statement.
	ExplainRunner func(ctx context.Context, db *sql.DB, sql string) ([][]any, error)

	// OnClose, if set, runs after the underlying *sql.DB is closed.
	// Used by the file driver to delete a temp on-disk SQLite database
	// once the connection is torn down.
	OnClose func() error

	// DatabaseListQuery, if set, returns a single-column list of user
	// database names for servers that host many (MSSQL, MySQL, Sybase).
	// Enables Conn.ListDatabases via DatabaseLister.
	DatabaseListQuery string

	// UseDatabaseStmt, if set, produces the statement that switches a
	// pinned connection's default database (MSSQL/Sybase: "USE [name]";
	// MySQL: "USE `name`"). Required together with DatabaseListQuery for
	// SchemaForDatabase to function.
	UseDatabaseStmt func(name string) string
}

// sqlQuerier is the subset of *sql.DB and *sql.Conn that the schema
// loaders need. Lets SchemaForDatabase run the same queries against a
// pinned *sql.Conn (so USE [db] state sticks) without duplicating the
// loader logic.
type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// OpenSQL wraps a *sql.DB as a db.Conn. Takes ownership of sqlDB.
func OpenSQL(ctx context.Context, sqlDB *sql.DB, opts SQLOptions) (Conn, error) {
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &sqlConn{db: sqlDB, opts: opts}, nil
}

type sqlConn struct {
	db   *sql.DB
	opts SQLOptions
}

func (c *sqlConn) Driver() string { return c.opts.DriverName }

func (c *sqlConn) Capabilities() Capabilities { return c.opts.Capabilities }

func (c *sqlConn) Schema(ctx context.Context) (*SchemaInfo, error) {
	info := &SchemaInfo{Status: map[string]ObjectKindStatus{}}
	if c.opts.SchemaQuery == "" {
		info.Status["tables"] = ObjectKindUnsupported
	} else {
		tables, err := c.loadTables(ctx)
		switch {
		case err == nil:
			info.Tables = tables
		case c.isDenied(err):
			info.Status["tables"] = ObjectKindDenied
		default:
			return nil, err
		}
	}

	if c.opts.RoutinesQuery == "" {
		info.Status["routines"] = ObjectKindUnsupported
	} else {
		routines, err := c.loadRoutines(ctx)
		switch {
		case err == nil:
			info.Routines = routines
		case c.isDenied(err):
			info.Status["routines"] = ObjectKindDenied
		default:
			return nil, err
		}
	}

	if c.opts.TriggersQuery == "" {
		info.Status["triggers"] = ObjectKindUnsupported
	} else {
		triggers, err := c.loadTriggers(ctx)
		switch {
		case err == nil:
			info.Triggers = triggers
		case c.isDenied(err):
			info.Status["triggers"] = ObjectKindDenied
		default:
			return nil, err
		}
	}
	return info, nil
}

func (c *sqlConn) isDenied(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPermissionDenied) {
		return true
	}
	if c.opts.IsPermissionDenied != nil && c.opts.IsPermissionDenied(err) {
		return true
	}
	return false
}

func (c *sqlConn) loadTables(ctx context.Context) ([]TableRef, error) {
	return loadTablesFrom(ctx, c.db, c.opts.SchemaQuery)
}

func loadTablesFrom(ctx context.Context, q sqlQuerier, query string) ([]TableRef, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("schema query: %w", err)
	}
	defer rows.Close()
	var out []TableRef
	for rows.Next() {
		var (
			schema, name     string
			isView, isSystem int
		)
		if err := rows.Scan(&schema, &name, &isView, &isSystem); err != nil {
			return nil, fmt.Errorf("schema scan: %w", err)
		}
		kind := TableKindTable
		if isView != 0 {
			kind = TableKindView
		}
		out = append(out, TableRef{Schema: schema, Name: name, Kind: kind, System: isSystem != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (c *sqlConn) loadRoutines(ctx context.Context) ([]RoutineRef, error) {
	return loadRoutinesFrom(ctx, c.db, c.opts.RoutinesQuery)
}

func loadRoutinesFrom(ctx context.Context, q sqlQuerier, query string) ([]RoutineRef, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("routines query: %w", err)
	}
	defer rows.Close()
	var out []RoutineRef
	for rows.Next() {
		var (
			schema, name, language string
			kindCode               string
			isSystem               int
		)
		var langNull sql.NullString
		if err := rows.Scan(&schema, &name, &kindCode, &langNull, &isSystem); err != nil {
			return nil, fmt.Errorf("routines scan: %w", err)
		}
		language = langNull.String
		rk := RoutineKindFunction
		switch kindCode {
		case "P", "p":
			rk = RoutineKindProcedure
		case "A", "a":
			rk = RoutineKindAggregate
		}
		out = append(out, RoutineRef{Schema: schema, Name: name, Kind: rk, Language: language, System: isSystem != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("routines rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (c *sqlConn) loadTriggers(ctx context.Context) ([]TriggerRef, error) {
	return loadTriggersFrom(ctx, c.db, c.opts.TriggersQuery)
}

func loadTriggersFrom(ctx context.Context, q sqlQuerier, query string) ([]TriggerRef, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("triggers query: %w", err)
	}
	defer rows.Close()
	var out []TriggerRef
	for rows.Next() {
		var (
			schema, table, name, timing, event string
			isSystem                           int
		)
		if err := rows.Scan(&schema, &table, &name, &timing, &event, &isSystem); err != nil {
			return nil, fmt.Errorf("triggers scan: %w", err)
		}
		out = append(out, TriggerRef{Schema: schema, Table: table, Name: name, Timing: timing, Event: event, System: isSystem != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("triggers rows: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Schema != out[j].Schema {
			return out[i].Schema < out[j].Schema
		}
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Columns runs ColumnsQuery or ColumnsBuilder. Returns nil when
// neither is configured (no error).
func (c *sqlConn) Columns(ctx context.Context, t TableRef) ([]Column, error) {
	var (
		query string
		args  []any
	)
	if c.opts.ColumnsBuilder != nil {
		query, args = c.opts.ColumnsBuilder(t)
	} else if c.opts.ColumnsQuery != "" {
		query = c.opts.ColumnsQuery
		args = []any{t.Schema, t.Name}
	} else {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("columns query %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var (
			name    string
			typeSQL sql.NullString
		)
		if err := rows.Scan(&name, &typeSQL); err != nil {
			return nil, fmt.Errorf("columns scan: %w", err)
		}
		out = append(out, Column{Name: name, TypeName: typeSQL.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("columns rows: %w", err)
	}
	return out, nil
}

// ColumnsIn satisfies DatabaseColumner. Pins a *sql.Conn, applies
// USE [database], then runs the driver's configured columns query on
// the pinned conn so drivers whose query is session-scoped (MSSQL's
// INFORMATION_SCHEMA.COLUMNS) return rows from the requested catalog
// rather than the connection's login default.
func (c *sqlConn) ColumnsIn(ctx context.Context, database string, t TableRef) ([]Column, error) {
	if database == "" || c.opts.UseDatabaseStmt == nil {
		return c.Columns(ctx, t)
	}
	useStmt := c.opts.UseDatabaseStmt(database)
	if useStmt == "" {
		return c.Columns(ctx, t)
	}
	var (
		query string
		args  []any
	)
	if c.opts.ColumnsBuilder != nil {
		query, args = c.opts.ColumnsBuilder(t)
	} else if c.opts.ColumnsQuery != "" {
		query = c.opts.ColumnsQuery
		args = []any{t.Schema, t.Name}
	} else {
		return nil, nil
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("columns pin: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, useStmt); err != nil {
		return nil, fmt.Errorf("columns use %q: %w", database, err)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("columns query %s.%s.%s: %w", database, t.Schema, t.Name, err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var (
			name    string
			typeSQL sql.NullString
		)
		if err := rows.Scan(&name, &typeSQL); err != nil {
			return nil, fmt.Errorf("columns scan: %w", err)
		}
		out = append(out, Column{Name: name, TypeName: typeSQL.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("columns rows: %w", err)
	}
	return out, nil
}

func (c *sqlConn) Definition(ctx context.Context, kind, schema, name string) (string, error) {
	if c.opts.DefinitionFetcher == nil {
		return "", ErrDefinitionUnsupported
	}
	return c.opts.DefinitionFetcher(ctx, c.db, kind, schema, name)
}

// Explain runs the engine's EXPLAIN flow and returns raw plan rows. The
// TUI dispatches on Capabilities.ExplainFormat to pick a parser. Adapters
// with ExplainFormatNone return ErrExplainUnsupported; adapters with a
// custom SQLOptions.ExplainRunner delegate to it (MSSQL pins a connection
// so SHOWPLAN_XML session state survives to the next batch).
func (c *sqlConn) Explain(ctx context.Context, query string) ([][]any, error) {
	format := c.opts.Capabilities.ExplainFormat
	if format == ExplainFormatNone {
		return nil, ErrExplainUnsupported
	}
	if c.opts.ExplainRunner != nil {
		return c.opts.ExplainRunner(ctx, c.db, query)
	}
	wrapped := wrapExplainSQL(format, query)
	rows, err := c.db.QueryContext(ctx, wrapped)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	return scanExplainRows(rows)
}

// ExplainIn satisfies DatabaseExplainer. Routes the explain through a
// pinned *sql.Conn with USE [database] applied first so session state
// outlives the wrap-and-query step. MSSQL's custom ExplainRunner does
// its own conn pinning; we prepend USE into the query string so it runs
// under the same SHOWPLAN_XML session.
func (c *sqlConn) ExplainIn(ctx context.Context, database, query string) ([][]any, error) {
	if database == "" || c.opts.UseDatabaseStmt == nil {
		return c.Explain(ctx, query)
	}
	format := c.opts.Capabilities.ExplainFormat
	if format == ExplainFormatNone {
		return nil, ErrExplainUnsupported
	}
	useStmt := c.opts.UseDatabaseStmt(database)
	if useStmt == "" {
		return c.Explain(ctx, query)
	}
	if c.opts.ExplainRunner != nil {
		return c.opts.ExplainRunner(ctx, c.db, useStmt+";\n"+query)
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("explain pin: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, useStmt); err != nil {
		return nil, fmt.Errorf("explain use: %w", err)
	}
	wrapped := wrapExplainSQL(format, query)
	rows, err := conn.QueryContext(ctx, wrapped)
	if err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	return scanExplainRows(rows)
}

func scanExplainRows(rows *sql.Rows) ([][]any, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("explain columns: %w", err)
	}
	var out [][]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("explain scan: %w", err)
		}
		for i, v := range dest {
			if b, ok := v.([]byte); ok {
				dest[i] = string(b)
			}
		}
		out = append(out, dest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("explain rows: %w", err)
	}
	return out, nil
}

// ListDatabases satisfies DatabaseLister. Returns ErrExplainUnsupported-
// shaped sentinel semantics via an explicit error when the driver didn't
// supply DatabaseListQuery (should never hit in practice -- callers
// type-assert and guard on SupportsCrossDatabase).
func (c *sqlConn) ListDatabases(ctx context.Context) ([]string, error) {
	if c.opts.DatabaseListQuery == "" {
		return nil, fmt.Errorf("list databases: unsupported by driver %s", c.opts.DriverName)
	}
	rows, err := c.db.QueryContext(ctx, c.opts.DatabaseListQuery)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("list databases scan: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list databases rows: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// UseDatabaseStmt satisfies DatabaseLister by exposing the driver's
// catalog-switch statement builder. Returns empty when the driver did
// not configure one; callers must feature-detect.
func (c *sqlConn) UseDatabaseStmt(name string) string {
	if c.opts.UseDatabaseStmt == nil {
		return ""
	}
	return c.opts.UseDatabaseStmt(name)
}

// SchemaForDatabase pins a connection, issues USE <database>, then runs
// the standard schema/routine/trigger queries on the pinned conn so the
// switched context survives. Requires both DatabaseListQuery and
// UseDatabaseStmt in SQLOptions.
func (c *sqlConn) SchemaForDatabase(ctx context.Context, database string) (*SchemaInfo, error) {
	if c.opts.UseDatabaseStmt == nil {
		return nil, fmt.Errorf("schema for database: unsupported by driver %s", c.opts.DriverName)
	}
	pinned, err := c.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("pin conn: %w", err)
	}
	defer pinned.Close()
	if _, err := pinned.ExecContext(ctx, c.opts.UseDatabaseStmt(database)); err != nil {
		return nil, fmt.Errorf("use %q: %w", database, err)
	}
	info := &SchemaInfo{Status: map[string]ObjectKindStatus{}}
	if c.opts.SchemaQuery == "" {
		info.Status["tables"] = ObjectKindUnsupported
	} else {
		tables, err := loadTablesFrom(ctx, pinned, c.opts.SchemaQuery)
		switch {
		case err == nil:
			info.Tables = tables
		case c.isDenied(err):
			info.Status["tables"] = ObjectKindDenied
		default:
			return nil, err
		}
	}
	if c.opts.RoutinesQuery == "" {
		info.Status["routines"] = ObjectKindUnsupported
	} else {
		routines, err := loadRoutinesFrom(ctx, pinned, c.opts.RoutinesQuery)
		switch {
		case err == nil:
			info.Routines = routines
		case c.isDenied(err):
			info.Status["routines"] = ObjectKindDenied
		default:
			return nil, err
		}
	}
	if c.opts.TriggersQuery == "" {
		info.Status["triggers"] = ObjectKindUnsupported
	} else {
		triggers, err := loadTriggersFrom(ctx, pinned, c.opts.TriggersQuery)
		switch {
		case err == nil:
			info.Triggers = triggers
		case c.isDenied(err):
			info.Status["triggers"] = ObjectKindDenied
		default:
			return nil, err
		}
	}
	return info, nil
}

func (c *sqlConn) Close() error {
	err := c.db.Close()
	if c.opts.OnClose != nil {
		if cerr := c.opts.OnClose(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

func (c *sqlConn) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

func (c *sqlConn) Exec(ctx context.Context, query string, args ...any) error {
	if _, err := c.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// Query returns a streaming Rows. Caller MUST Close() or the
// statement holds the connection. Column metadata is fetched up
// front so headers render before rows stream.
func (c *sqlConn) Query(ctx context.Context, query string) (Rows, error) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("column types: %w", err)
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{
			Name:     t.Name(),
			TypeName: t.DatabaseTypeName(),
		}
	}
	return &sqlRows{rows: rows, cols: cols}, nil
}

// sqlRows adapts *sql.Rows to db.Rows. Each Scan allocates a
// fresh []any so callers can buffer rows safely. []byte -> string
// to avoid RawBytes lifetime traps.
type sqlRows struct {
	rows   *sql.Rows
	cols   []Column
	closed bool
	err    error
}

func (r *sqlRows) Columns() []Column { return r.cols }

func (r *sqlRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	return r.rows.Next()
}

func (r *sqlRows) Scan() ([]any, error) {
	if r.closed {
		return nil, fmt.Errorf("scan: rows closed")
	}
	dest := make([]any, len(r.cols))
	ptrs := make([]any, len(r.cols))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		r.err = fmt.Errorf("scan: %w", err)
		return nil, r.err
	}
	// []byte -> string so text columns display cleanly.
	for i, v := range dest {
		if b, ok := v.([]byte); ok {
			dest[i] = string(b)
		}
	}
	return dest, nil
}

func (r *sqlRows) Err() error {
	if r.err != nil {
		return r.err
	}
	if r.rows == nil {
		return nil
	}
	return r.rows.Err()
}

// Close tears down the cursor. Idempotent.
func (r *sqlRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.rows.Close()
}

// NextResultSet advances *sql.Rows to the next result set and refreshes
// the cached column descriptors. Drivers that don't produce multiple
// result sets return false immediately.
func (r *sqlRows) NextResultSet() bool {
	if r.closed || r.err != nil {
		return false
	}
	if !r.rows.NextResultSet() {
		return false
	}
	types, err := r.rows.ColumnTypes()
	if err != nil {
		r.err = fmt.Errorf("column types: %w", err)
		return false
	}
	cols := make([]Column, len(types))
	for i, t := range types {
		cols[i] = Column{
			Name:     t.Name(),
			TypeName: t.DatabaseTypeName(),
		}
	}
	r.cols = cols
	return true
}
