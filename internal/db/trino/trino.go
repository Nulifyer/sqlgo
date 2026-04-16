// Package trino registers the trinodb/trino-go-client driver (pure Go,
// HTTP/REST protocol). Import for side effects.
//
// Trino (forked from PrestoSQL) serves as a federated query engine. One
// "catalog" corresponds roughly to a data source (Hive, Iceberg, MySQL,
// Postgres, ...) and each catalog has its own information_schema. Our
// Config model binds cfg.Database to the session catalog at DSN build
// time, so the explorer lists schemas under that one catalog -- cross-
// catalog queries still work via 3-part names but are not browsed.
package trino

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/trinodb/trino-go-client/trino"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "trino"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(HTTPTransport)
	db.Register(preset{})
}

// Profile is the Trino dialect brain. Trino has no stored procedures or
// triggers exposed through information_schema, no CREATE OR REPLACE for
// tables (views have CREATE OR REPLACE VIEW), and a DOUBLE-QUOTE identifier
// quote matching ANSI SQL. Transactions exist but are session-scoped and
// most deployments disable them at the coordinator -- we treat the engine
// as non-transactional for the autocomplete/safety overlay.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectTrino,
		SupportsTransactions: false,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// HTTPTransport wraps the trino-go-client HTTP driver. The coordinator
// port defaults to 8080 on most deployments; TLS (https) flips to 8443
// on the server side but we keep DefaultPort at 8080 and let Options
// drive the scheme via "ssl".
var HTTPTransport = db.Transport{
	Name:          "trino",
	SQLDriverName: "trino",
	DefaultPort:   8080,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, HTTPTransport, cfg)
}

// schemaQuery lists tables and views in the current catalog's
// information_schema. table_type is either 'BASE TABLE' or 'VIEW'.
// System schemas (information_schema + the trino-specific metadata
// schemas exposed by each connector) are flagged is_system=1.
const schemaQuery = `
SELECT
    table_schema  AS schema_name,
    table_name    AS name,
    CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN table_schema IN ('information_schema', 'jdbc', 'metadata', 'runtime') THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
ORDER BY table_schema, table_name
`

// columnsQuery returns ordered columns for one table from the current
// catalog's information_schema. trino-go-client accepts ? placeholders.
const columnsQuery = `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position
`

// fetchDefinition runs SHOW CREATE TABLE|VIEW on the qualified object.
// Trino returns one row with a single column containing the DDL text.
// Procedures/functions/triggers are not exposed through DDL, so those
// kinds return the unsupported sentinel.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var keyword string
	switch kind {
	case "view":
		keyword = "VIEW"
	case "table":
		keyword = "TABLE"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	qualified := quoteIdent(schema) + "." + quoteIdent(name)
	q := fmt.Sprintf("SHOW CREATE %s %s", keyword, qualified)
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q).Scan(&body); err != nil {
		return "", fmt.Errorf("show create %s %s: %w", keyword, qualified, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for %s %s.%s", kind, schema, name)
	}
	return strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in double quotes (ANSI / Trino default).
// Embedded quotes are doubled.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// buildDSN produces an http(s):// URL accepted by trino-go-client.
//
// Field mapping (sqlgo -> trino-go-client DSN):
//
//	cfg.Host / cfg.Port                 -> URL host:port
//	cfg.User                            -> URL userinfo (required)
//	cfg.Password                        -> URL userinfo (only honored under https)
//	cfg.Database                        -> ?catalog=    (session catalog)
//	cfg.Options["schema"]               -> ?schema=     (default schema)
//	cfg.Options["ssl"] / ["secure"]     -> http vs https scheme
//	cfg.Options["access_token"]         -> ?accessToken= (JWT for OAuth2)
//	cfg.Options["ssl_cert_path"]        -> ?SSLCertPath= (custom CA file)
//	cfg.Options["source"]               -> ?source=     (client identifier header)
//	cfg.Options["kerberos_*"]           -> ?Kerberos... (keytab auth)
//	cfg.Options["query_timeout"]        -> ?query_timeout=
//	cfg.Options["session_properties"]   -> ?session_properties=
//
// Unknown Options keys pass through as-is so future driver knobs work
// without a buildDSN patch. Basic auth works only under https because
// trino-go-client refuses to send a password over plaintext.
func buildDSN(cfg db.Config) string {
	scheme := "http"
	switch strings.ToLower(strings.TrimSpace(cfg.Options["ssl"])) {
	case "true", "1", "yes", "on":
		scheme = "https"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Options["secure"])) {
	case "true", "1", "yes", "on":
		scheme = "https"
	}

	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		if scheme == "https" {
			port = 8443
		} else {
			port = 8080
		}
	}

	user := cfg.User
	if user == "" {
		// trino-go-client requires a user identifier for the
		// X-Trino-User header. "sqlgo" is a reasonable default when
		// the deployment has no real auth configured.
		user = "sqlgo"
	}

	var userinfo *url.Userinfo
	if cfg.Password != "" {
		userinfo = url.UserPassword(user, cfg.Password)
	} else {
		userinfo = url.User(user)
	}

	u := url.URL{
		Scheme: scheme,
		User:   userinfo,
		Host:   host + ":" + strconv.Itoa(port),
	}

	q := u.Query()
	if cfg.Database != "" {
		q.Set("catalog", cfg.Database)
	}

	// Semantic option mapping. Already-set keys win over raw passthrough.
	semantic := map[string]string{
		"schema":             "schema",
		"access_token":       "accessToken",
		"ssl_cert_path":      "SSLCertPath",
		"ssl_cert":           "SSLCert",
		"source":             "source",
		"query_timeout":      "query_timeout",
		"session_properties": "session_properties",
		"extra_credentials":  "extra_credentials",
		"roles":              "roles",
		"client_tags":        "clientTags",
		"custom_client":      "custom_client",

		"kerberos_enabled":             "KerberosEnabled",
		"kerberos_keytab_path":         "KerberosKeytabPath",
		"kerberos_principal":           "KerberosPrincipal",
		"kerberos_realm":               "KerberosRealm",
		"kerberos_config_path":         "KerberosConfigPath",
		"kerberos_remote_service_name": "KerberosRemoteServiceName",
	}
	for src, dst := range semantic {
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			q.Set(dst, v)
		}
	}

	// Pass through any remaining Options untouched. Skip the scheme toggle
	// (consumed above) and any key we've already emitted.
	skip := map[string]struct{}{"ssl": {}, "secure": {}}
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
