// Package sqlite registers the mattn/go-sqlite3 driver. Requires cgo.
// Import for side effects.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

// quoteSQLiteLiteral escapes s for PRAGMA table_info, which takes
// a literal not a bind value. Defensive: table names come from
// sqlite_master, not user input.
func quoteSQLiteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

const (
	driverName      = "sqlite"
	syntheticSchema = "main" // sqlite's implicit schema name
)

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(SQLiteTransport)
	db.Register(preset{})
}

var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthFlat,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          false,
		ExplainFormat:        db.ExplainFormatSQLiteRows,
		Dialect:              sqltok.DialectSQLite,
		SupportsTransactions: true,
	},
	SchemaQuery:   schemaQuery,
	TriggersQuery: triggersQuery,
	ColumnsBuilder: func(t db.TableRef) (string, []any) {
		q := "SELECT name, type FROM pragma_table_info(" + quoteSQLiteLiteral(t.Name) + ");"
		return q, nil
	},
	DefinitionFetcher:  fetchDefinition,
	TableDesignFetcher: fetchTableDesign,
}

var SQLiteTransport = db.Transport{
	Name:          "sqlite3",
	SQLDriverName: "sqlite3",
	DefaultPort:   0,
	SupportsTLS:   false,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, SQLiteTransport, cfg)
}

// schemaQuery lists user + system tables/views under the synthetic
// "main" schema. sqlite_% objects are flagged is_system=1 so the
// explorer groups them under Sys.
const schemaQuery = `
SELECT
    'main' AS schema_name,
    name,
    CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN name LIKE 'sqlite_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type IN ('table','view')
ORDER BY name;
`

// triggersQuery: user triggers via sqlite_master. SQL body is parsed
// loosely for timing (BEFORE/AFTER/INSTEAD OF) and event (INSERT/UPDATE/DELETE).
const triggersQuery = `
SELECT
    'main' AS schema_name,
    IFNULL(tbl_name, '') AS table_name,
    name   AS name,
    CASE
        WHEN UPPER(sql) LIKE '%INSTEAD OF%' THEN 'INSTEAD OF'
        WHEN UPPER(sql) LIKE '%BEFORE%'     THEN 'BEFORE'
        ELSE 'AFTER'
    END AS timing,
    CASE
        WHEN UPPER(sql) LIKE '%INSERT%' THEN 'INSERT'
        WHEN UPPER(sql) LIKE '%UPDATE%' THEN 'UPDATE'
        WHEN UPPER(sql) LIKE '%DELETE%' THEN 'DELETE'
        ELSE ''
    END AS event,
    CASE WHEN name LIKE 'sqlite_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type = 'trigger'
ORDER BY name;
`

// fetchDefinition retrieves the stored CREATE text from sqlite_master for
// views and triggers and prepends a DROP IF EXISTS. sqlite has no stored
// procedures or functions, so those kinds return ErrDefinitionUnsupported.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var masterType, dropKw string
	switch kind {
	case "view":
		masterType, dropKw = "view", "VIEW"
	case "trigger":
		masterType, dropKw = "trigger", "TRIGGER"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	_ = schema // sqlite is flat; schema ignored beyond the synthetic "main"
	var body sql.NullString
	err := sqlDB.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = ? AND name = ?",
		masterType, name).Scan(&body)
	if err != nil {
		return "", fmt.Errorf("sqlite_master: %w", err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for %s %s", kind, name)
	}
	drop := fmt.Sprintf("DROP %s IF EXISTS \"%s\";\n", dropKw, strings.ReplaceAll(name, `"`, `""`))
	return drop + strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

func fetchTableDesign(ctx context.Context, q db.SQLQuerier, t db.TableRef) (db.TableDesign, error) {
	_ = t.Schema
	cols, err := fetchSQLiteColumnDetails(ctx, q, t.Name)
	if err != nil {
		return db.TableDesign{}, err
	}
	if len(cols) == 0 {
		return db.TableDesign{Table: t}, nil
	}
	fkCols, err := sqliteForeignKeyColumns(ctx, q, t.Name)
	if err != nil {
		return db.TableDesign{}, err
	}
	uniqueCols, err := sqliteUniqueColumns(ctx, q, t.Name)
	if err != nil {
		return db.TableDesign{}, err
	}
	for i := range cols {
		cols[i].ForeignKey = fkCols[cols[i].Name]
		cols[i].Unique = uniqueCols[cols[i].Name]
	}
	return db.TableDesign{Table: t, Columns: cols}, nil
}

func fetchSQLiteColumnDetails(ctx context.Context, q db.SQLQuerier, table string) ([]db.ColumnDetail, error) {
	rows, err := q.QueryContext(ctx, "SELECT cid, name, type, [notnull], dflt_value, pk, hidden FROM pragma_table_xinfo("+quoteSQLiteLiteral(table)+") ORDER BY cid;")
	if err != nil {
		return nil, fmt.Errorf("table_xinfo: %w", err)
	}
	defer rows.Close()
	var out []db.ColumnDetail
	for rows.Next() {
		var (
			cid, notNull, pk, hidden int
			name, typ                string
			def                      sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk, &hidden); err != nil {
			return nil, fmt.Errorf("table_xinfo scan: %w", err)
		}
		d := db.ColumnDetail{
			Name:          name,
			TypeName:      typ,
			Ordinal:       cid + 1,
			NullableKnown: true,
			Nullable:      notNull == 0 && pk == 0,
			PrimaryKey:    pk > 0,
			Computed:      hidden == 2 || hidden == 3,
		}
		if def.Valid {
			d.DefaultKnown = true
			d.Default = def.String
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("table_xinfo rows: %w", err)
	}
	return out, nil
}

func sqliteForeignKeyColumns(ctx context.Context, q db.SQLQuerier, table string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT [from] FROM pragma_foreign_key_list("+quoteSQLiteLiteral(table)+");")
	if err != nil {
		return nil, fmt.Errorf("foreign_key_list: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("foreign_key_list scan: %w", err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("foreign_key_list rows: %w", err)
	}
	return out, nil
}

func sqliteUniqueColumns(ctx context.Context, q db.SQLQuerier, table string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT name, [unique] FROM pragma_index_list("+quoteSQLiteLiteral(table)+");")
	if err != nil {
		return nil, fmt.Errorf("index_list: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		var unique int
		if err := rows.Scan(&name, &unique); err != nil {
			return nil, fmt.Errorf("index_list scan: %w", err)
		}
		if unique != 0 {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index_list rows: %w", err)
	}
	out := map[string]bool{}
	for _, name := range names {
		idxRows, err := q.QueryContext(ctx, "SELECT name FROM pragma_index_info("+quoteSQLiteLiteral(name)+");")
		if err != nil {
			return nil, fmt.Errorf("index_info %s: %w", name, err)
		}
		for idxRows.Next() {
			var col string
			if err := idxRows.Scan(&col); err != nil {
				_ = idxRows.Close()
				return nil, fmt.Errorf("index_info scan: %w", err)
			}
			out[col] = true
		}
		if err := idxRows.Err(); err != nil {
			_ = idxRows.Close()
			return nil, fmt.Errorf("index_info rows: %w", err)
		}
		_ = idxRows.Close()
	}
	return out, nil
}

// buildDSN converts cfg into a sqlite DSN. cfg.Database is the
// file path; empty or ":memory:" → in-memory. cfg.Options becomes
// URI query params (e.g. _pragma=journal_mode(wal)).
func buildDSN(cfg db.Config) string {
	path := strings.TrimSpace(cfg.Database)
	if path == "" || path == ":memory:" {
		return ":memory:"
	}
	if len(cfg.Options) == 0 {
		return path
	}
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{
		Scheme:   "file",
		Opaque:   path,
		RawQuery: q.Encode(),
	}
	return u.String()
}
