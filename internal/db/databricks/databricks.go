// Package databricks registers the databricks/databricks-sql-go driver
// (pure Go, Thrift over HTTPS). Import for side effects.
//
// Databricks SQL is cloud-only: there is no self-hostable image, so
// this adapter has no compose service or seed entry. The integration
// test is env-gated and SKIPs when SQLGO_IT_DATABRICKS_* vars aren't
// populated (see databricks_integration_test.go).
//
// Config mapping:
//
//	cfg.Host                     -> workspace host (xy.cloud.databricks.com)
//	cfg.Port                     -> HTTPS port (default 443)
//	cfg.User                     -> ignored by driver (token in Password)
//	cfg.Password                 -> personal access token (or OAuth secret)
//	cfg.Database                 -> default catalog (required)
//	cfg.Options["http_path"]     -> warehouse/endpoint path (required)
//	cfg.Options["schema"]        -> default schema within the catalog
//	cfg.Options["authType"]      -> Pat | OAuthM2M | OauthU2M (default Pat)
//	cfg.Options["clientID"]      -> OAuth M2M service-principal id
//	cfg.Options["clientSecret"]  -> OAuth M2M service-principal secret
//	cfg.Options["timeout"]       -> connect timeout (seconds)
//	cfg.Options["maxRows"]       -> per-page row cap (driver default 100000)
//	cfg.Options["userAgentEntry"]-> appended to User-Agent
//	cfg.Options["useCloudFetch"] -> "true"/"false" (driver default true)
//
// Databricks supports `catalog.schema.table` refs so cross-catalog is
// possible in principle, but a connection is pinned to one default
// catalog and the explorer lists one catalog -- match the Snowflake/
// Trino/Vertica pattern: SchemaDepthSchemas + SupportsCrossDatabase
// false. Transactions are acknowledged by the driver but Databricks
// SQL has no multi-statement ACID semantics, so SupportsTransactions
// stays false.
package databricks

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/databricks/databricks-sql-go"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "databricks"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the Databricks dialect brain. Databricks SQL is Spark
// SQL with Delta Lake extensions -- backtick identifiers by default,
// LIMIT/OFFSET, standard information_schema. EXPLAIN returns a text
// plan (no JSON variant) so ExplainFormat stays None.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '`',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectDatabricks,
		SupportsTransactions: false,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps databricks-sql-go. DefaultPort=443 matches the
// driver's canonical HTTPS endpoint.
var NativeTransport = db.Transport{
	Name:          "databricks",
	SQLDriverName: "databricks",
	DefaultPort:   443,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, NativeTransport, cfg)
}

// schemaQuery lists tables and views from the connection's default
// catalog's information_schema. `table_type` is 'VIEW' for views and
// 'BASE TABLE' / 'MANAGED' / 'EXTERNAL' for tables. The only system
// schema is INFORMATION_SCHEMA itself.
const schemaQuery = `
SELECT table_schema AS schema_name,
       table_name   AS name,
       CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view,
       CASE WHEN table_schema = 'information_schema' THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
ORDER BY table_schema, table_name
`

// columnsQuery returns ordered columns for one table from the current
// catalog's information_schema. databricks-sql-go supports ? as the
// positional placeholder.
const columnsQuery = `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position
`

// fetchDefinition runs SHOW CREATE TABLE, which also emits the view
// DDL when the object is a view (Databricks treats views as a table
// kind). Identifiers are backtick-quoted to tolerate spaces and
// reserved words.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	if kind != "view" {
		return "", db.ErrDefinitionUnsupported
	}
	qualified := quoteIdent(schema) + "." + quoteIdent(name)
	q := fmt.Sprintf("SHOW CREATE TABLE %s", qualified)
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q).Scan(&body); err != nil {
		return "", fmt.Errorf("show create view %s: %w", qualified, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for view %s", qualified)
	}
	return strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in backticks (Spark SQL / Databricks
// default). Embedded backticks are doubled per Spark SQL grammar.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// buildDSN produces a DSN accepted by databricks-sql-go:
//
//	token:<pat>@<host>:<port>/<http_path>?catalog=...&schema=...
//
// No URL scheme is prefixed -- the driver parses the form above
// directly. Field mapping (sqlgo -> databricks-sql-go DSN):
//
//	cfg.User / cfg.Password         -> userinfo. When authType is "Pat"
//	                                   (default), Password is the PAT
//	                                   and User is literally "token".
//	                                   For OAuth modes, userinfo is
//	                                   omitted and credentials go in
//	                                   the query string.
//	cfg.Host / cfg.Port             -> Host:Port
//	cfg.Options["http_path"]        -> URL path
//	cfg.Database                    -> ?catalog=
//	cfg.Options["schema"]           -> ?schema=
//	cfg.Options["authType"]         -> ?authType=
//	cfg.Options["clientID"]         -> ?clientID=
//	cfg.Options["clientSecret"]     -> ?clientSecret=
//	cfg.Options["timeout"]          -> ?timeout=
//	cfg.Options["maxRows"]          -> ?maxRows=
//	cfg.Options["userAgentEntry"]   -> ?userAgentEntry=
//	cfg.Options["useCloudFetch"]    -> ?useCloudFetch=
//
// Unknown Options keys pass through verbatim so future driver knobs
// work without a buildDSN patch.
func buildDSN(cfg db.Config) string {
	authType := strings.TrimSpace(cfg.Options["authType"])

	var userinfo *url.Userinfo
	// PAT mode (default when unset): userinfo is "token:<pat>". The
	// driver treats the literal username "token" as the PAT sentinel.
	if authType == "" || strings.EqualFold(authType, "Pat") {
		if cfg.Password != "" {
			userinfo = url.UserPassword("token", cfg.Password)
		} else if cfg.User != "" {
			userinfo = url.UserPassword(cfg.User, cfg.Password)
		}
	}

	host := cfg.Host
	port := cfg.Port
	if port == 0 {
		port = 443
	}
	if host != "" {
		host = host + ":" + strconv.Itoa(port)
	}

	// http_path is required at connect-time but we leave validation to
	// the driver so the user gets a clear sql.Open error instead of a
	// sqlgo-side surprise. Normalize so the DSN has exactly one slash
	// between host and path.
	httpPath := strings.TrimSpace(cfg.Options["http_path"])
	if httpPath != "" && !strings.HasPrefix(httpPath, "/") {
		httpPath = "/" + httpPath
	}

	u := url.URL{
		User: userinfo,
		Host: host,
		Path: httpPath,
	}

	q := u.Query()

	// catalog is Databricks's "database" -- bind from cfg.Database.
	if cfg.Database != "" {
		q.Set("catalog", cfg.Database)
	}

	// Semantic option mapping. Most keys pass through with the driver's
	// native spelling; listed here so they participate in the skip set.
	semantic := map[string]string{
		"schema":         "schema",
		"authType":       "authType",
		"accessToken":    "accessToken",
		"clientID":       "clientID",
		"clientSecret":   "clientSecret",
		"timeout":        "timeout",
		"maxRows":        "maxRows",
		"userAgentEntry": "userAgentEntry",
		"useCloudFetch":  "useCloudFetch",
		"catalog":        "catalog",
	}
	skip := map[string]struct{}{"http_path": {}}
	for src, dst := range semantic {
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			q.Set(dst, v)
		}
		skip[src] = struct{}{}
	}

	// OAuth M2M: if authType=OAuthM2M but clientID/Secret weren't set
	// in Options, fall back to User/Password so the form still carries
	// credentials. For OauthU2M there are no credentials to pass.
	if strings.EqualFold(authType, "OAuthM2M") {
		if q.Get("clientID") == "" && cfg.User != "" {
			q.Set("clientID", cfg.User)
		}
		if q.Get("clientSecret") == "" && cfg.Password != "" {
			q.Set("clientSecret", cfg.Password)
		}
	}

	// Pass through any remaining Options untouched.
	for k, v := range cfg.Options {
		if v == "" {
			continue
		}
		if _, drop := skip[k]; drop {
			continue
		}
		if q.Has(k) {
			continue
		}
		q.Set(k, v)
	}

	u.RawQuery = q.Encode()
	// databricks-sql-go wants the DSN WITHOUT a scheme prefix. url.URL
	// emits "//host/path?q" when Scheme is empty, so strip the leading
	// // -- same trick as the Snowflake adapter.
	s := u.String()
	s = strings.TrimPrefix(s, "//")
	return s
}
