// Package vertica registers the vertica/vertica-sql-go driver (pure Go,
// native Vertica wire protocol). Import for side effects.
//
// Vertica's SQL frontend is descended from Postgres, so most ANSI grammar
// applies (double-quote identifiers, LIMIT N tail, EXPLAIN/SHOW). The
// catalog lives under the v_catalog schema rather than information_schema.
// Vertica supports cross-schema queries within one database but a
// connection is pinned to a single database (like Postgres), so we model
// SchemaDepth: Schemas with SupportsCrossDatabase: false.
package vertica

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	vertigo "github.com/vertica/vertica-sql-go"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "vertica"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the Vertica dialect brain. Vertica's SQL surface is the
// closest of any non-Postgres engine to vanilla Postgres -- ANSI quoting,
// LIMIT/OFFSET, RETURNING, CTEs -- but its EXPLAIN output is a custom
// text format (no JSON option) so ExplainFormat stays None until the TUI
// grows a Vertica-aware parser.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectVertica,
		SupportsTransactions: true,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps the vertica-sql-go driver. Default port 5433
// is Vertica's well-known native-protocol port.
//
// Open is wired so mTLS / custom-TLS options can funnel through
// vertigo.RegisterTLSConfig -- the driver does not accept client cert
// paths in the DSN; it only accepts a named *tls.Config registered
// via that API, then referenced as tlsmode=<name>. Basic TLS modes
// (server, server-strict, none, prefer) still flow through buildDSN.
var NativeTransport = db.Transport{
	Name:          "vertica",
	SQLDriverName: "vertica",
	DefaultPort:   5433,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
	Open:          openVertica,
}

// openVertica is the Transport.Open entry point. It registers a
// custom *tls.Config with vertica-sql-go when sqlgo-native TLS
// options are present, then opens via sql.Open with the DSN rewritten
// to reference the registered name. Otherwise it falls through to
// plain sql.Open on the base DSN.
func openVertica(ctx context.Context, cfg db.Config) (*sql.DB, func() error, error) {
	dsn, err := buildDSNWithTLS(cfg)
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := sql.Open("vertica", dsn)
	return sqlDB, nil, err
}

// buildDSNWithTLS returns the DSN to pass to sql.Open, handling the
// custom TLS path via RegisterTLSConfig when needed. If no sqlgo-side
// mTLS/custom-TLS options are set, the plain buildDSN output is
// returned unchanged so the existing tlsmode=server/none/... modes
// stay on their fast path.
func buildDSNWithTLS(cfg db.Config) (string, error) {
	tlsCfg, err := buildCustomTLSConfig(cfg.Options)
	if err != nil {
		return "", err
	}
	base := buildDSN(cfg)
	if tlsCfg == nil {
		return base, nil
	}
	name := tlsConfigName(cfg)
	if err := vertigo.RegisterTLSConfig(name, tlsCfg); err != nil {
		return "", fmt.Errorf("register vertica tls: %w", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse vertica dsn: %w", err)
	}
	q := u.Query()
	q.Set("tlsmode", name)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildCustomTLSConfig assembles a *tls.Config from the sqlgo-native
// TLS option keys. Returns (nil, nil) when none are present -- the
// caller should skip registration in that case.
//
// Options (snake_case on the sqlgo side):
//
//	tls_cert_file            -> client certificate PEM (mTLS)
//	tls_key_file             -> client private key PEM (mTLS)
//	tls_ca_file              -> CA bundle PEM (verify server)
//	tls_server_name          -> SNI / verify hostname override
//	tls_insecure_skip_verify -> disable verify (testing only)
func buildCustomTLSConfig(opts map[string]string) (*tls.Config, error) {
	certFile := strings.TrimSpace(opts["tls_cert_file"])
	keyFile := strings.TrimSpace(opts["tls_key_file"])
	caFile := strings.TrimSpace(opts["tls_ca_file"])
	serverName := strings.TrimSpace(opts["tls_server_name"])
	skipVerify := boolOpt(opts["tls_insecure_skip_verify"])

	if certFile == "" && keyFile == "" && caFile == "" && serverName == "" && !skipVerify {
		return nil, nil
	}
	if (certFile == "") != (keyFile == "") {
		return nil, errors.New("vertica mTLS requires both tls_cert_file and tls_key_file")
	}

	cfg := &tls.Config{InsecureSkipVerify: skipVerify}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load vertica client cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read vertica ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("vertica ca file %q had no certs", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// tlsConfigName returns a deterministic, unique name for the given
// TLS config inputs. RegisterTLSConfig overwrites entries with the
// same name, so reconnecting with the same settings is idempotent.
func tlsConfigName(cfg db.Config) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s|%v",
		cfg.Host, cfg.Port,
		cfg.Options["tls_cert_file"],
		cfg.Options["tls_key_file"],
		cfg.Options["tls_ca_file"],
		cfg.Options["tls_server_name"],
		boolOpt(cfg.Options["tls_insecure_skip_verify"]))
	return "sqlgo-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// boolOpt parses the truthy subset sqlgo accepts across option maps.
func boolOpt(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, NativeTransport, cfg)
}

// schemaQuery merges v_catalog.tables (is_view=0) and v_catalog.views
// (is_view=1). The v_* schemas are Vertica's system catalogs; flagging
// them is_system=1 lets the explorer bucket them under Sys.
const schemaQuery = `
SELECT table_schema AS schema_name,
       table_name   AS name,
       0            AS is_view,
       CASE WHEN table_schema IN ('v_catalog','v_internal','v_monitor','v_func','v_txtindex')
            THEN 1 ELSE 0 END AS is_system
FROM v_catalog.tables
UNION ALL
SELECT table_schema AS schema_name,
       table_name   AS name,
       1            AS is_view,
       CASE WHEN table_schema IN ('v_catalog','v_internal','v_monitor','v_func','v_txtindex')
            THEN 1 ELSE 0 END AS is_system
FROM v_catalog.views
ORDER BY schema_name, name
`

// columnsQuery returns ordered columns for one table from v_catalog.
// vertica-sql-go uses ? as the placeholder.
const columnsQuery = `
SELECT column_name, data_type
FROM v_catalog.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position
`

// fetchDefinition returns runnable DDL for a view by reading
// v_catalog.views.view_definition. Tables/procedures/functions are
// not exposed via a portable DDL view in v_catalog (EXPORT_OBJECTS
// is a meta-function with side effects on the server log dir), so
// only views are supported here.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	if kind != "view" {
		return "", db.ErrDefinitionUnsupported
	}
	const q = `
SELECT view_definition
FROM v_catalog.views
WHERE table_schema = ? AND table_name = ?
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

// quoteIdent wraps identifiers in double quotes (ANSI / Vertica default).
// Embedded quotes are doubled.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// buildDSN produces a vertica:// URL accepted by vertica-sql-go.
//
// Field mapping (sqlgo -> vertica-sql-go DSN):
//
//	cfg.Host / cfg.Port           -> URL host:port
//	cfg.User / cfg.Password       -> URL userinfo
//	cfg.Database                  -> URL path (/<db>)
//	cfg.Options["tlsmode"]        -> ?tlsmode=          (none|server|server-strict|custom)
//	cfg.Options["backup_server_node"]    -> ?backup_server_node=
//	cfg.Options["autocommit"]            -> ?autocommit=
//	cfg.Options["use_prepared_stmts"]    -> ?use_prepared_stmts=
//	cfg.Options["binary_parameters"]     -> ?binary_parameters=
//	cfg.Options["client_label"]          -> ?client_label=
//	cfg.Options["connection_load_balance"] -> ?connection_load_balance=
//	cfg.Options["workload"]              -> ?workload=
//	cfg.Options["oauth_access_token"]    -> ?oauth_access_token=
//	cfg.Options["kerberos_service_name"] -> ?kerberos_service_name=
//	cfg.Options["kerberos_host"]         -> ?kerberos_host=
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
		port = 5433
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
		Scheme: "vertica",
		User:   userinfo,
		Host:   host + ":" + strconv.Itoa(port),
	}
	if cfg.Database != "" {
		// url.URL escapes the path; database names rarely contain
		// special chars but this keeps the DSN well-formed if they do.
		u.Path = "/" + cfg.Database
	}

	q := u.Query()

	// Known options pass through unchanged -- vertica-sql-go accepts
	// snake_case query parameters directly. We list them explicitly so
	// the absent-test cases in vertica_test.go can verify nothing is
	// silently mistransformed.
	known := map[string]struct{}{
		"tlsmode":                 {},
		"backup_server_node":      {},
		"autocommit":              {},
		"use_prepared_stmts":      {},
		"binary_parameters":       {},
		"client_label":            {},
		"connection_load_balance": {},
		"workload":                {},
		"oauth_access_token":      {},
		"kerberos_service_name":   {},
		"kerberos_host":           {},
	}
	for k := range known {
		if v := strings.TrimSpace(cfg.Options[k]); v != "" {
			q.Set(k, v)
		}
	}
	// Pass through any remaining Options untouched.
	for k, v := range cfg.Options {
		if v == "" {
			continue
		}
		if _, ok := known[k]; ok {
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
