// Package spanner registers the googleapis/go-sql-spanner driver
// (pure Go, gRPC over HTTPS to Cloud Spanner, or plain-text gRPC to
// the local emulator). Import for side effects.
//
// Spanner has both a cloud service and a self-hostable emulator
// (gcr.io/cloud-spanner-emulator/emulator). The compose service runs
// the emulator on host port 19010 (in-container 9010) and the
// integration test targets it with autoConfigEmulator=true, which
// both points the gRPC client at the emulator and auto-creates the
// target instance + database on first use.
//
// Config mapping:
//
//	cfg.Host                              -> optional host prefix (emulator only)
//	cfg.Port                              -> optional port       (emulator only)
//	cfg.User                              -> ignored by driver (no password auth)
//	cfg.Password                          -> ignored
//	cfg.Database                          -> DATABASE id in projects/.../databases/<x>
//	cfg.Options["project"]                -> PROJECT id (required)
//	cfg.Options["instance"]               -> INSTANCE id (required)
//	cfg.Options["autoConfigEmulator"]     -> ;autoConfigEmulator=true
//	cfg.Options["credentials"]            -> ;credentials=/path/to/key.json
//	cfg.Options["credentials_json"]       -> ;credentialsJson=...
//	cfg.Options["use_plain_text"]         -> ;usePlainText=true (emulator-style gRPC)
//	cfg.Options["num_channels"]           -> ;numChannels=
//	cfg.Options["retry_aborts_internally"]-> ;retryAbortsInternally=
//	cfg.Options["min_sessions"]           -> ;minSessions=
//	cfg.Options["max_sessions"]           -> ;maxSessions=
//	cfg.Options["database_role"]          -> ;databaseRole=
//	cfg.Options["dialect"]                -> ;dialect=postgresql (toggles PG-dialect db)
//	cfg.Options["user_agent"]             -> ;userAgent=
//
// A Spanner connection is pinned to one projects/P/instances/I/
// databases/D path, so SupportsCrossDatabase stays false. Read/write
// transactions are first-class (Begin/Commit/Rollback round-trip DML),
// so SupportsTransactions is true. DDL runs outside of transactions
// (Spanner commits DDL asynchronously) but sqlgo's transaction model
// doesn't care about that -- DDL in a tx just errors at execute time.
package spanner

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/googleapis/go-sql-spanner"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "spanner"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(NativeTransport)
	db.Register(preset{})
}

// Profile is the Spanner dialect brain. Spanner speaks GoogleSQL
// (formerly "Standard SQL") with backtick identifier quoting,
// LIMIT/OFFSET, and an UPPER_SNAKE_CASE INFORMATION_SCHEMA. A per-
// database PostgreSQL-dialect mode exists, but the keyword set stays
// ANSI-compatible enough that DialectSpanner is the right tag for
// both modes.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:           db.SchemaDepthSchemas,
		LimitSyntax:           db.LimitSyntaxLimit,
		IdentifierQuote:       '`',
		SupportsCancel:        true,
		SupportsTLS:           true,
		ExplainFormat:         db.ExplainFormatNone,
		Dialect:               sqltok.DialectSpanner,
		SupportsTransactions:  true,
		SupportsCrossDatabase: false,
	},
	SchemaQuery:       schemaQuery,
	ColumnsQuery:      columnsQuery,
	DefinitionFetcher: fetchDefinition,
}

// NativeTransport wraps go-sql-spanner. DefaultPort=0 because the
// canonical production DSN has no host/port at all -- only the
// projects/.../databases/... path. For emulator mode the user sets
// Host + Port and autoConfigEmulator=true; buildDSN renders the
// leading "host:port/" prefix then.
var NativeTransport = db.Transport{
	Name:          "spanner",
	SQLDriverName: "spanner",
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

// schemaQuery lists tables and views from INFORMATION_SCHEMA. Spanner
// puts user objects in TABLE_SCHEMA = ” (the empty string is the
// default/unnamed schema), system objects in 'INFORMATION_SCHEMA' and
// 'SPANNER_SYS'. TABLE_TYPE is 'BASE TABLE' | 'VIEW' | 'SYNONYM'.
// Ordering nulls/empty first so the user schema sorts before system.
const schemaQuery = `
SELECT IFNULL(TABLE_SCHEMA, '')                  AS schema_name,
       TABLE_NAME                                AS name,
       CASE WHEN TABLE_TYPE = 'VIEW' THEN 1 ELSE 0 END AS is_view,
       CASE WHEN TABLE_SCHEMA IN ('INFORMATION_SCHEMA','SPANNER_SYS')
            THEN 1 ELSE 0 END                    AS is_system
FROM INFORMATION_SCHEMA.TABLES
ORDER BY TABLE_SCHEMA, TABLE_NAME
`

// columnsQuery returns ordered columns for one table from
// INFORMATION_SCHEMA.COLUMNS. go-sql-spanner accepts both ? positional
// and @name placeholders; ? keeps the shared columnsQuery contract
// aligned with the other profiles.
const columnsQuery = `
SELECT COLUMN_NAME, SPANNER_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION
`

// fetchDefinition returns a view's DDL from INFORMATION_SCHEMA.VIEWS.
// Spanner doesn't expose SHOW CREATE for tables, and procedures /
// triggers aren't a first-class object, so those kinds return the
// unsupported sentinel.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	if kind != "view" {
		return "", db.ErrDefinitionUnsupported
	}
	q := `SELECT VIEW_DEFINITION
FROM INFORMATION_SCHEMA.VIEWS
WHERE TABLE_SCHEMA = @schema AND TABLE_NAME = @name`
	var body sql.NullString
	if err := sqlDB.QueryRowContext(ctx, q,
		sql.Named("schema", schema),
		sql.Named("name", name),
	).Scan(&body); err != nil {
		return "", fmt.Errorf("view_definition %s.%s: %w", schema, name, err)
	}
	if !body.Valid || strings.TrimSpace(body.String) == "" {
		return "", fmt.Errorf("no definition for view %s.%s", schema, name)
	}
	qualified := quoteIdent(name)
	if schema != "" {
		qualified = quoteIdent(schema) + "." + qualified
	}
	return "CREATE VIEW " + qualified + " SQL SECURITY INVOKER AS\n" +
		strings.TrimRight(body.String, "\r\n\t ;") + ";", nil
}

// quoteIdent wraps identifiers in backticks (GoogleSQL grammar).
// Embedded backticks are doubled.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// buildDSN produces a DSN accepted by go-sql-spanner. The base form is:
//
//	projects/<PROJECT>/instances/<INSTANCE>/databases/<DB>
//
// optionally prefixed with "host:port/" for the emulator, and
// optionally followed by ";key=value;..." params. Params use `;` as
// the separator (not `&`) and the driver rejects `?` inside the path.
//
// Required keys: Options["project"], Options["instance"]. Database
// comes from cfg.Database (falls back to Options["database"] for
// callers that prefer to keep everything in Options).
//
// Empty strings for project/instance/database produce placeholder
// segments so the driver's own Open error message surfaces cleanly
// instead of sqlgo guessing.
func buildDSN(cfg db.Config) string {
	project := firstNonEmpty(cfg.Options["project"], cfg.Options["project_id"])
	instance := firstNonEmpty(cfg.Options["instance"], cfg.Options["instance_id"])
	database := firstNonEmpty(cfg.Database, cfg.Options["database"])

	var b strings.Builder
	if host := strings.TrimSpace(cfg.Host); host != "" {
		b.WriteString(host)
		if cfg.Port > 0 {
			b.WriteString(":")
			b.WriteString(strconv.Itoa(cfg.Port))
		}
		b.WriteString("/")
	}
	b.WriteString("projects/")
	b.WriteString(project)
	b.WriteString("/instances/")
	b.WriteString(instance)
	b.WriteString("/databases/")
	b.WriteString(database)

	// Semantic option mapping -> driver's native spelling. Everything
	// already consumed into the URL base goes into skip.
	semantic := map[string]string{
		"autoConfigEmulator":      "autoConfigEmulator",
		"credentials":             "credentials",
		"credentials_json":        "credentialsJson",
		"use_plain_text":          "usePlainText",
		"num_channels":            "numChannels",
		"retry_aborts_internally": "retryAbortsInternally",
		"min_sessions":            "minSessions",
		"max_sessions":            "maxSessions",
		"database_role":           "databaseRole",
		"dialect":                 "dialect",
		"user_agent":              "userAgent",
		"optimizer_version":       "optimizerVersion",
		"optimizer_stats_package": "optimizerStatisticsPackage",
		"rpc_priority":            "rpcPriority",
	}
	skip := map[string]struct{}{
		"project":     {},
		"project_id":  {},
		"instance":    {},
		"instance_id": {},
		"database":    {},
	}
	// Stable iteration across the semantic map is not needed for
	// driver behavior (the driver splits key=val pairs unordered) but
	// we want deterministic DSNs for tests. Collect then sort.
	params := make(map[string]string)
	for src, dst := range semantic {
		skip[src] = struct{}{}
		if v := strings.TrimSpace(cfg.Options[src]); v != "" {
			params[dst] = v
		}
	}
	// Pass through any remaining Options untouched. Key name stays as-
	// is so future driver knobs work without a buildDSN patch.
	for k, v := range cfg.Options {
		if v == "" {
			continue
		}
		if _, drop := skip[k]; drop {
			continue
		}
		if _, set := params[k]; set {
			continue
		}
		params[k] = v
	}

	if len(params) > 0 {
		keys := sortedKeys(params)
		b.WriteString(";")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(";")
			}
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(encodeParamValue(params[k]))
		}
	}

	return b.String()
}

// encodeParamValue percent-encodes characters that would otherwise
// confuse the driver's param splitter ('=' and ';') or break line-
// oriented consumers ('\n'). Other characters pass through verbatim
// so paths and JSON credentials stay readable in the DSN.
func encodeParamValue(v string) string {
	// url.QueryEscape over-escapes (turns spaces into '+', escapes
	// slashes) -- that's fine for a DSN and guarantees round-tripping
	// through shells and logs.
	return url.QueryEscape(v)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// tiny inline sort to avoid pulling "sort" for a cold-path helper
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
