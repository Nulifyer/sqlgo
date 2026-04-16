// Package snowflake registers the snowflakedb/gosnowflake driver
// (pure Go, Snowflake Arrow/JSON REST protocol over HTTPS). Import
// for side effects.
//
// Snowflake is a cloud-only warehouse: there is no self-hostable
// image, so this adapter has no compose service or seed entry. The
// integration test is env-gated and SKIPs when SQLGO_IT_SNOWFLAKE_*
// vars aren't populated (see snowflake_integration_test.go).
//
// Config mapping:
//
//	cfg.Host                -> account identifier (e.g. xy12345.us-east-1)
//	cfg.Port                -> optional host port override (rarely used)
//	cfg.User / cfg.Password -> username + password (or key passphrase)
//	cfg.Database            -> target database (required)
//	cfg.Options["schema"]   -> default schema
//	cfg.Options["warehouse"]-> compute warehouse (required for most auth)
//	cfg.Options["role"]     -> session role
//	cfg.Options["authenticator"] -> snowflake, oauth, externalbrowser,
//	                                 https://<okta>, jwt, username_password_mfa
//	cfg.Options["private_key_path"] -> JWT key-pair auth PEM path
//	cfg.Options["application"]      -> X-Snowflake-Application header
//	cfg.Options["passcode"]         -> MFA passcode
//	cfg.Options["token"]            -> OAuth bearer token
//	cfg.Options["region"]           -> legacy account region override
//
// Snowflake supports fully-qualified `db.schema.table` refs so cross-
// database is possible in principle, but a connection is pinned to
// one default database and the explorer lists one catalog -- match
// the Trino/Vertica pattern: SchemaDepthSchemas + SupportsCrossDatabase
// false.
package snowflake

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/snowflakedb/gosnowflake"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "snowflake"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the Snowflake dialect brain. Snowflake implements ANSI
// SQL with LIMIT/OFFSET, double-quote identifiers, CTEs, stored
// procedures (JavaScript / SQL / Python), and transactions. EXPLAIN
// returns a text plan, not JSON, so ExplainFormat stays None.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectSnowflake,
		SupportsTransactions: true,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps gosnowflake. DefaultPort=0 because the DSN
// format doesn't require a port (HTTPS on 443 is implied by the
// account identifier).
var NativeTransport = db.Transport{
	Name:          "snowflake",
	SQLDriverName: "snowflake",
	DefaultPort:   0,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, NativeTransport, cfg)
}

// schemaQuery lists tables and views from the current database's
// information_schema. Snowflake's information_schema is scoped to
// the connection's default database. INFORMATION_SCHEMA is the
// only system schema; everything else is user-visible (PUBLIC,
// ACCOUNT_USAGE proxies, etc. live under SNOWFLAKE database which
// isn't the current DB).
const schemaQuery = `
SELECT table_schema AS schema_name,
       table_name   AS name,
       CASE WHEN table_type = 'VIEW' THEN 1 ELSE 0 END AS is_view,
       CASE WHEN table_schema = 'INFORMATION_SCHEMA' THEN 1 ELSE 0 END AS is_system
FROM information_schema.tables
ORDER BY table_schema, table_name
`

// columnsQuery returns ordered columns for one table from the
// current database's information_schema. gosnowflake supports ?
// positional placeholders.
const columnsQuery = `
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position
`

// fetchDefinition uses GET_DDL to reproduce the view DDL. Snowflake
// exposes GET_DDL for most object kinds (table, view, sequence,
// procedure, function, pipe) but only 'view' is portable enough to
// be worth rendering in the definition pane; other kinds return the
// unsupported sentinel.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	if kind != "view" {
		return "", db.ErrDefinitionUnsupported
	}
	// GET_DDL takes a string object-kind and a string qualified name.
	// Wrap both with single quotes, doubled for embedded quotes.
	qualified := quoteIdent(schema) + "." + quoteIdent(name)
	q := fmt.Sprintf("SELECT GET_DDL('VIEW', %s)", sqlQuote(qualified))
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q).Scan(&body); err != nil {
		return "", fmt.Errorf("get_ddl view %s: %w", qualified, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for view %s", qualified)
	}
	return strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in double quotes (ANSI / Snowflake).
// Embedded quotes are doubled. Snowflake is case-sensitive inside
// quoted identifiers; the caller is responsible for upper-casing
// when the stored identifier is unquoted (Snowflake upper-cases
// unquoted names at DDL time).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqlQuote wraps a string literal in single quotes, doubling any
// embedded single quotes. Used for embedding identifiers into
// GET_DDL's string arguments.
func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// buildDSN produces a DSN accepted by gosnowflake. The driver has
// two recognized forms; we emit the account-style form:
//
//	user[:password]@account/database/schema?warehouse=...&role=...
//
// Field mapping (sqlgo -> gosnowflake DSN):
//
//	cfg.User / cfg.Password                -> userinfo
//	cfg.Host                               -> account identifier (e.g. xy12345.us-east-1)
//	cfg.Port                               -> :port override (rare; kept for gateway setups)
//	cfg.Database                           -> /<database>
//	cfg.Options["schema"]                  -> /<schema> (after database)
//	cfg.Options["warehouse"]               -> ?warehouse=
//	cfg.Options["role"]                    -> ?role=
//	cfg.Options["authenticator"]           -> ?authenticator=
//	cfg.Options["private_key_path"]        -> ?privateKeyFile=
//	cfg.Options["application"]             -> ?application=
//	cfg.Options["passcode"]                -> ?passcode=
//	cfg.Options["passcode_in_password"]    -> ?passcodeInPassword=
//	cfg.Options["token"]                   -> ?token=
//	cfg.Options["region"]                  -> ?region= (legacy region override)
//	cfg.Options["login_timeout"]           -> ?loginTimeout=
//	cfg.Options["request_timeout"]         -> ?requestTimeout=
//	cfg.Options["client_session_keep_alive"] -> ?clientSessionKeepAlive=
//	cfg.Options["insecure_mode"]           -> ?insecureMode= (dev only)
//
// Unknown Options keys pass through verbatim so future driver knobs
// work without a buildDSN patch.
func buildDSN(cfg db.Config) string {
	user := cfg.User
	var userinfo *url.Userinfo
	if user != "" {
		if cfg.Password != "" {
			userinfo = url.UserPassword(user, cfg.Password)
		} else {
			userinfo = url.User(user)
		}
	}

	host := cfg.Host
	if cfg.Port != 0 {
		host = host + ":" + strconv.Itoa(cfg.Port)
	}

	// Database + optional schema compose the URL path. gosnowflake
	// accepts an absent database when authenticator-only validation
	// is enough (e.g. externalbrowser first-run), so we don't force
	// it here -- db.OpenWith is the one that sanity-checks required
	// fields via engineSpec.requiredCore.
	var path string
	if cfg.Database != "" {
		path = "/" + cfg.Database
		if schema := strings.TrimSpace(cfg.Options["schema"]); schema != "" {
			path += "/" + schema
		}
	}

	u := url.URL{
		User: userinfo,
		Host: host,
		Path: path,
	}

	q := u.Query()

	// Semantic option mapping. snake_case on the sqlgo side,
	// gosnowflake's camelCase / snake_case mix on the wire.
	semantic := map[string]string{
		"warehouse":                 "warehouse",
		"role":                      "role",
		"authenticator":             "authenticator",
		"private_key_path":          "privateKeyFile",
		"private_key_passphrase":    "privateKeyFilePwd",
		"application":               "application",
		"passcode":                  "passcode",
		"passcode_in_password":      "passcodeInPassword",
		"token":                     "token",
		"region":                    "region",
		"login_timeout":             "loginTimeout",
		"request_timeout":           "requestTimeout",
		"client_session_keep_alive": "clientSessionKeepAlive",
		"insecure_mode":             "insecureMode",
		"ocsp_fail_open":            "ocspFailOpen",
		"tracing":                   "tracing",
		"disable_oob_telemetry":     "disableOCSPChecks",
	}
	// "schema" is consumed into the path above; skip it in both the
	// semantic map and the raw passthrough.
	skip := map[string]struct{}{"schema": {}}
	for src, dst := range semantic {
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			q.Set(dst, v)
		}
		skip[src] = struct{}{}
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
	// gosnowflake wants the DSN WITHOUT a scheme prefix. url.URL.String()
	// emits "//host/path?q" when Scheme is empty, so strip the leading //.
	s := u.String()
	s = strings.TrimPrefix(s, "//")
	return s
}
