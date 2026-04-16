// Package hana registers the SAP/go-hdb driver (pure Go, HANA SQL
// wire protocol over TLS). Import for side effects.
//
// SAP HANA is an in-memory column store. Its SQL surface is close to
// ANSI (quoted identifiers, LIMIT/OFFSET, standard DML) but every
// metadata table lives under the SYS schema, not information_schema.
// Cross-database queries only exist under the multi-tenant (MDC)
// feature and are not used here -- a single connection is pinned to
// one tenant DB via the `databaseName` option.
package hana

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	hdbdriver "github.com/SAP/go-hdb/driver"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "hana"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the HANA dialect brain. HANA supports transactions (with
// autocommit on by default), has stored procedures and calculation
// views, but EXPLAIN_PLAN writes to SYS tables rather than returning
// a plan inline, so ExplainFormat stays None here.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectHANA,
		SupportsTransactions: true,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps go-hdb. Default port 39017 is HANA Express's
// tenant DB SQL port (instance 90, system DB on 39013). Standard HANA
// installs use 3<instance>15 / 3<instance>13; override via cfg.Port.
// NativeTransport routes through openHANA so non-basic auth modes (JWT,
// X.509 client cert) can go via go-hdb's Connector API. Basic auth
// still takes the DSN path inside openHANA so the "Other..." picker
// and existing tests keep working.
var NativeTransport = db.Transport{
	Name:          "hana",
	SQLDriverName: "hdb",
	DefaultPort:   39017,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
	Open:          openHANA,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, NativeTransport, cfg)
}

// authMode returns the normalized auth_method option: "basic", "jwt",
// or "x509". Blank / unknown values map to "basic" so existing configs
// behave unchanged.
func authMode(opts map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(opts["auth_method"])) {
	case "jwt":
		return "jwt"
	case "x509", "cert", "client_cert":
		return "x509"
	default:
		return "basic"
	}
}

// openHANA is the Transport.Open entry point. For basic auth it falls
// back to the DSN path. For JWT / X.509 it constructs a go-hdb
// Connector so those auth modes (which the DSN parser does not expose)
// can take effect via sql.OpenDB.
func openHANA(ctx context.Context, cfg db.Config) (*sql.DB, func() error, error) {
	mode := authMode(cfg.Options)
	if mode == "basic" {
		sqlDB, err := sql.Open("hdb", buildDSN(cfg))
		return sqlDB, nil, err
	}
	connector, err := buildConnector(mode, cfg)
	if err != nil {
		return nil, nil, err
	}
	return sql.OpenDB(connector), nil, nil
}

// buildConnector wires a go-hdb Connector for JWT / X.509 auth. The
// TLS / tenant DB / default schema / tuning options are reapplied onto
// the Connector since those no longer flow through a DSN string.
func buildConnector(mode string, cfg db.Config) (*hdbdriver.Connector, error) {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 39017
	}
	addr := host + ":" + strconv.Itoa(port)

	var (
		c   *hdbdriver.Connector
		err error
	)
	switch mode {
	case "jwt":
		token, err := resolveJWTToken(cfg.Options, cfg.Password)
		if err != nil {
			return nil, err
		}
		c = hdbdriver.NewJWTAuthConnector(addr, token)
	case "x509":
		certFile := strings.TrimSpace(cfg.Options["client_cert_file"])
		keyFile := strings.TrimSpace(cfg.Options["client_key_file"])
		if certFile == "" || keyFile == "" {
			return nil, errors.New("x509 auth requires client_cert_file and client_key_file")
		}
		c, err = hdbdriver.NewX509AuthConnectorByFiles(addr, certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("x509 connector: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported auth_method %q", mode)
	}

	if cfg.Database != "" {
		c = c.WithDatabase(cfg.Database)
	}
	if v := strings.TrimSpace(cfg.Options["default_schema"]); v != "" {
		c.SetDefaultSchema(v)
	}

	serverName := strings.TrimSpace(cfg.Options["tls_server_name"])
	skipVerify := boolOpt(cfg.Options["tls_insecure_skip_verify"])
	caFile := strings.TrimSpace(cfg.Options["tls_root_ca_file"])
	if serverName != "" || skipVerify || caFile != "" {
		var caFiles []string
		if caFile != "" {
			caFiles = []string{caFile}
		}
		if err := c.SetTLS(serverName, skipVerify, caFiles...); err != nil {
			return nil, fmt.Errorf("hana tls: %w", err)
		}
	}

	if v := strings.TrimSpace(cfg.Options["timeout"]); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil {
			c.SetTimeout(d)
		}
	}
	if v := strings.TrimSpace(cfg.Options["ping_interval"]); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil {
			c.SetPingInterval(d)
		}
	}
	if v := strings.TrimSpace(cfg.Options["locale"]); v != "" {
		c.SetLocale(v)
	}
	if v := strings.TrimSpace(cfg.Options["fetch_size"]); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			c.SetFetchSize(n)
		}
	}
	if v := strings.TrimSpace(cfg.Options["bulk_size"]); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			c.SetBulkSize(n)
		}
	}
	if v := strings.TrimSpace(cfg.Options["lob_chunk_size"]); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			c.SetLobChunkSize(n)
		}
	}
	if v := strings.TrimSpace(cfg.Options["dfv"]); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			c.SetDfv(n)
		}
	}
	return c, nil
}

// resolveJWTToken returns the JWT bearer in priority order:
//  1. cfg.Options["jwt_token"] — inline token (UI / secrets manager).
//  2. cfg.Options["jwt_token_file"] — read the file off disk.
//  3. cfg.Password — convention fallback so the password field can
//     carry a token when the form has no explicit token slot.
//
// Trailing whitespace (including the newline most token files end on)
// is stripped so the wire value is exact.
func resolveJWTToken(opts map[string]string, password string) (string, error) {
	if v := strings.TrimSpace(opts["jwt_token"]); v != "" {
		return v, nil
	}
	if path := strings.TrimSpace(opts["jwt_token_file"]); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read jwt_token_file: %w", err)
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", fmt.Errorf("jwt_token_file %q is empty", path)
		}
		return token, nil
	}
	if v := strings.TrimSpace(password); v != "" {
		return v, nil
	}
	return "", errors.New("jwt auth requires jwt_token, jwt_token_file, or a password containing the token")
}

// boolOpt parses the truthy subset sqlgo accepts across option maps.
func boolOpt(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// schemaQuery merges SYS.TABLES and SYS.VIEWS. HANA keeps them in
// separate catalog views (unlike ANSI information_schema), so UNION
// ALL gives the explorer one flat list. System schemas include SYS,
// SYSTEM, PUBLIC, and anything prefixed with an underscore (_SYS_*,
// _SYS_BIC, _SYS_REPO, ...) which HANA uses for runtime-generated
// metadata objects.
const schemaQuery = `
SELECT SCHEMA_NAME AS schema_name,
       TABLE_NAME  AS name,
       0           AS is_view,
       CASE WHEN SCHEMA_NAME IN ('SYS','SYSTEM','PUBLIC','UIS','HANA_XS_BASE')
              OR SCHEMA_NAME LIKE '\_SYS%' ESCAPE '\'
            THEN 1 ELSE 0 END AS is_system
FROM SYS.TABLES
UNION ALL
SELECT SCHEMA_NAME AS schema_name,
       VIEW_NAME   AS name,
       1           AS is_view,
       CASE WHEN SCHEMA_NAME IN ('SYS','SYSTEM','PUBLIC','UIS','HANA_XS_BASE')
              OR SCHEMA_NAME LIKE '\_SYS%' ESCAPE '\'
            THEN 1 ELSE 0 END AS is_system
FROM SYS.VIEWS
ORDER BY schema_name, name
`

// columnsQuery merges SYS.TABLE_COLUMNS and SYS.VIEW_COLUMNS so both
// kinds return ordered column info from one query. go-hdb uses ? as
// the placeholder; $1/:name do not apply.
const columnsQuery = `
SELECT COLUMN_NAME, DATA_TYPE_NAME
FROM SYS.TABLE_COLUMNS
WHERE SCHEMA_NAME = ? AND TABLE_NAME = ?
UNION ALL
SELECT COLUMN_NAME, DATA_TYPE_NAME
FROM SYS.VIEW_COLUMNS
WHERE SCHEMA_NAME = ? AND VIEW_NAME = ?
ORDER BY 1
`

// fetchDefinition returns runnable DDL for a view from SYS.VIEWS.
// DEFINITION. Table/procedure DDL requires the SYS_EXPORT.* meta-
// procedures which have file-system side effects on the HANA host,
// so those kinds return the unsupported sentinel.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	if kind != "view" {
		return "", db.ErrDefinitionUnsupported
	}
	const q = `
SELECT DEFINITION
FROM SYS.VIEWS
WHERE SCHEMA_NAME = ? AND VIEW_NAME = ?
`
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q, schema, name).Scan(&body); err != nil {
		return "", fmt.Errorf("read view definition %s.%s: %w", schema, name, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for view %s.%s", schema, name)
	}
	header := fmt.Sprintf("CREATE OR REPLACE VIEW %s AS\n", quoteIdent(schema)+"."+quoteIdent(name))
	return header + strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in double quotes (ANSI / HANA default).
// Embedded quotes are doubled. HANA is case-sensitive inside quotes,
// so upper-casing is the caller's responsibility.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// buildDSN produces a hdb:// URL accepted by go-hdb.
//
// Field mapping (sqlgo -> go-hdb DSN):
//
//	cfg.Host / cfg.Port                 -> URL host:port
//	cfg.User / cfg.Password             -> URL userinfo
//	cfg.Database                        -> ?databaseName= (MDC tenant selector)
//	cfg.Options["default_schema"]       -> ?defaultSchema=
//	cfg.Options["tls_server_name"]      -> ?TLSServerName=
//	cfg.Options["tls_insecure_skip_verify"] -> ?TLSInsecureSkipVerify=
//	cfg.Options["tls_root_ca_file"]     -> ?TLSRootCAFile=
//	cfg.Options["locale"]               -> ?locale=
//	cfg.Options["dfv"]                  -> ?dfv= (data format version)
//	cfg.Options["ping_interval"]        -> ?pingInterval=
//	cfg.Options["timeout"]              -> ?timeout=
//	cfg.Options["fetch_size"]           -> ?fetchSize=
//	cfg.Options["bulk_size"]            -> ?bulkSize=
//	cfg.Options["lob_chunk_size"]       -> ?lobChunkSize=
//
// Unknown Options keys pass through verbatim so future driver knobs
// work without a buildDSN patch.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 39017
	}

	var userinfo *url.Userinfo
	if cfg.User != "" {
		if cfg.Password != "" {
			userinfo = url.UserPassword(cfg.User, cfg.Password)
		} else {
			userinfo = url.User(cfg.User)
		}
	}

	u := url.URL{
		Scheme: "hdb",
		User:   userinfo,
		Host:   host + ":" + strconv.Itoa(port),
	}

	q := u.Query()
	if cfg.Database != "" {
		// Tenant DB selector for MDC systems. Ignored on single-tenant.
		q.Set("databaseName", cfg.Database)
	}

	// Semantic option mapping. snake_case on the sqlgo side, the HANA
	// driver's camelCase on the wire.
	semantic := map[string]string{
		"default_schema":           "defaultSchema",
		"tls_server_name":          "TLSServerName",
		"tls_insecure_skip_verify": "TLSInsecureSkipVerify",
		"tls_root_ca_file":         "TLSRootCAFile",
		"locale":                   "locale",
		"dfv":                      "dfv",
		"ping_interval":            "pingInterval",
		"timeout":                  "timeout",
		"fetch_size":               "fetchSize",
		"bulk_size":                "bulkSize",
		"lob_chunk_size":           "lobChunkSize",
	}
	for src, dst := range semantic {
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			q.Set(dst, v)
		}
	}

	// Pass through any remaining Options untouched.
	skip := map[string]struct{}{}
	for src := range semantic {
		skip[src] = struct{}{}
	}
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
	return u.String()
}
