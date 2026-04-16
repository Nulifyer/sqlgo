// Package clickhouse registers the ClickHouse/clickhouse-go/v2 driver
// (pure Go, native TCP protocol). Import for side effects.
//
// Auth surface (on top of plain user/password):
//
//	cfg.Options["tls_cert_file"]            -> client certificate PEM (mTLS)
//	cfg.Options["tls_key_file"]             -> client private key PEM (mTLS)
//	cfg.Options["tls_ca_file"]              -> CA bundle PEM (verify server)
//	cfg.Options["tls_server_name"]          -> SNI / verify-hostname override
//	cfg.Options["tls_insecure_skip_verify"] -> disable verify (testing only)
//
// ClickHouse offers both TCP (9000) and TCP+TLS (9440). The custom-TLS
// path cannot be expressed in a DSN query string (clickhouse-go/v2's
// *tls.Config is a programmatic field), so when any tls_* option is
// set sqlgo parses the DSN, attaches the *tls.Config, and opens via
// clickhouse.OpenDB. Plain "?secure=true" without a custom config stays
// on the fast DSN path.
package clickhouse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	chgo "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "clickhouse"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the ClickHouse dialect brain. CH has no CREATE OR REPLACE
// for most objects, no transactions, no triggers, and no user-defined
// stored procedures in the traditional sense. Databases act as the
// single grouping level -- there's no schema layer beneath them -- so
// we treat CH databases as the "schema" dimension and expose every DB's
// tables in one flat listing (databases are lightweight namespaces, and
// CH supports 2-part `db.table` references from any session).
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '`',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectClickhouse,
		SupportsTransactions: false,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps clickhouse-go/v2 over the native TCP protocol
// (port 9000). HTTP (8123) exists but native is more efficient and is
// the driver's default scheme.
//
// Open is wired so the custom-TLS path (mTLS, private CA, SNI override)
// can attach a *tls.Config to clickhouse.Options and open via OpenDB --
// the plain DSN can't carry a programmatic tls.Config.
var NativeTransport = db.Transport{
	Name:          "clickhouse",
	SQLDriverName: "clickhouse",
	DefaultPort:   9000,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
	Open:          openClickHouse,
}

// openClickHouse is the Transport.Open entry point. When any sqlgo-side
// TLS option (tls_cert_file / tls_key_file / tls_ca_file / tls_server_name
// / tls_insecure_skip_verify) is set, we parse the generated DSN into
// *clickhouse.Options, attach the constructed *tls.Config, and hand back
// a *sql.DB via clickhouse.OpenDB. Otherwise we fall through to the
// standard sql.Open("clickhouse", dsn) path so the fast DSN route keeps
// working unchanged.
func openClickHouse(ctx context.Context, cfg db.Config) (*sql.DB, func() error, error) {
	tlsCfg, err := buildCustomTLSConfig(cfg.Options)
	if err != nil {
		return nil, nil, err
	}
	dsn := buildDSN(cfg)
	if tlsCfg == nil {
		sqlDB, err := sql.Open("clickhouse", dsn)
		return sqlDB, nil, err
	}
	opts, err := chgo.ParseDSN(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse clickhouse dsn: %w", err)
	}
	opts.TLS = tlsCfg
	return chgo.OpenDB(opts), nil, nil
}

// buildCustomTLSConfig assembles a *tls.Config from the sqlgo-native
// TLS option keys. Returns (nil, nil) when none are present so the
// caller can skip OpenDB and stay on the DSN fast path.
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
		return nil, errors.New("clickhouse mTLS requires both tls_cert_file and tls_key_file")
	}

	cfg := &tls.Config{InsecureSkipVerify: skipVerify}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load clickhouse client cert: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read clickhouse ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("clickhouse ca file %q had no certs", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
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

// schemaQuery lists all user+system tables/views across every database
// visible to the user. `database` becomes the explorer's schema node.
// MaterializedView / LiveView / View engines are flagged is_view=1. The
// .inner* and .inner_id.* tables backing materialized views are hidden.
const schemaQuery = `
SELECT
    database AS schema_name,
    name,
    CASE WHEN engine LIKE '%View' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN database IN ('system', 'INFORMATION_SCHEMA', 'information_schema') THEN 1 ELSE 0 END AS is_system
FROM system.tables
WHERE name NOT LIKE '.inner%'
  AND is_temporary = 0
ORDER BY database, name
`

// columnsQuery returns ordered columns for one table from system.columns.
// The clickhouse-go/v2 driver accepts ? placeholders over native TCP.
const columnsQuery = `
SELECT name, type
FROM system.columns
WHERE database = ? AND table = ?
ORDER BY position
`

// fetchDefinition uses SHOW CREATE <kind> <db>.<name>. ClickHouse emits
// a full, runnable DDL string in one column. Tables and views share the
// same code path; the kind arg is used only to gate the verb so
// "procedure" / "trigger" return the unsupported sentinel cleanly.
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

// quoteIdent wraps an identifier in backticks (ClickHouse's native
// quote). Embedded backticks are doubled defensively -- valid CH names
// can't contain them but the escape matches the standard practice.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// buildDSN produces a clickhouse:// URL accepted by clickhouse-go/v2.
// cfg.Options passes through as query params ("secure=true",
// "compress=lz4", "dial_timeout=5s", etc.). sqlgo-native TLS option
// keys (tls_cert_file / tls_key_file / tls_ca_file / tls_server_name
// / tls_insecure_skip_verify) are stripped -- clickhouse-go routes
// unknown params into server-side SETTINGS and would reject them on
// handshake. Those options are consumed by openClickHouse instead.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 9000
	}

	u := url.URL{
		Scheme: "clickhouse",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   host + ":" + strconv.Itoa(port),
	}
	if cfg.Database != "" {
		u.Path = "/" + cfg.Database
	}
	q := u.Query()
	for k, v := range cfg.Options {
		if _, drop := sqlgoTLSOptionKeys[k]; drop {
			continue
		}
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// sqlgoTLSOptionKeys are consumed by openClickHouse to construct a
// *tls.Config; they must not be forwarded into the driver's DSN.
var sqlgoTLSOptionKeys = map[string]struct{}{
	"tls_cert_file":            {},
	"tls_key_file":             {},
	"tls_ca_file":              {},
	"tls_server_name":          {},
	"tls_insecure_skip_verify": {},
}
