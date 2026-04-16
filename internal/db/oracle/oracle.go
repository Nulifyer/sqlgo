// Package oracle registers sijms/go-ora/v2. Import for side effects.
//
// cfg.Database is interpreted as the Oracle service name. Identifiers are
// stored uppercase when unquoted, so schema/table strings coming from
// db.TableRef are used as-is — the schema loader already returns them in
// their stored form.
package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	goora "github.com/sijms/go-ora/v2"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "oracle"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(GoOraTransport)
	db.Register(preset{})
}

var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthSchemas,
		LimitSyntax:          db.LimitSyntaxFetchFirst,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          true,
		ExplainFormat:        db.ExplainFormatNone,
		Dialect:              sqltok.DialectOracle,
		SupportsTransactions: true,
	},
	SchemaQuery:        schemaQuery,
	ColumnsQuery:       columnsQuery,
	RoutinesQuery:      routinesQuery,
	TriggersQuery:      triggersQuery,
	IsPermissionDenied: isPermissionDenied,
	DefinitionFetcher:  fetchDefinition,
}

var GoOraTransport = db.Transport{
	Name:          "goora",
	SQLDriverName: "oracle",
	DefaultPort:   1521,
	SupportsTLS:   true,
	BuildDSN:      buildDSN,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, GoOraTransport, cfg)
}

// isPermissionDenied detects ORA-01031 (insufficient privileges) and a
// handful of adjacent access errors that show up when a user can't see
// a dictionary view.
func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "ORA-01031"): // insufficient privileges
		return true
	case strings.Contains(msg, "ORA-00942"): // table or view does not exist
		return true
	case strings.Contains(msg, "ORA-01749"): // you may not GRANT/REVOKE to yourself
		return true
	}
	return false
}

// Oracle stores identifiers uppercase by default. We use ALL_* dictionary
// views so non-DBA users see what they have access to. Common
// Oracle-maintained schemas get flagged as system so the explorer groups
// them under Sys; APEX_* / FLOWS_* are caught by prefix.
const schemaQuery = `
SELECT
    owner               AS schema_name,
    table_name          AS name,
    0                   AS is_view,
    CASE WHEN owner IN ('SYS','SYSTEM','OUTLN','XDB','DBSNMP','APPQOSSYS','ORDSYS','CTXSYS','MDSYS','WMSYS',
                        'LBACSYS','OLAPSYS','ORDDATA','DVSYS','GSMADMIN_INTERNAL','AUDSYS','GSMCATUSER',
                        'GSMUSER','ANONYMOUS','SI_INFORMTN_SCHEMA','ORDPLUGINS','MDDATA','ORACLE_OCM',
                        'EXFSYS','PUBLIC','REMOTE_SCHEDULER_AGENT','DBSFWUSER','GGSYS','SYSBACKUP',
                        'SYSDG','SYSKM','SYSRAC','DIP','XS$NULL')
              OR owner LIKE 'APEX_%' OR owner LIKE 'FLOWS_%'
         THEN 1 ELSE 0 END AS is_system
FROM all_tables
UNION ALL
SELECT
    owner, view_name, 1,
    CASE WHEN owner IN ('SYS','SYSTEM','OUTLN','XDB','DBSNMP','APPQOSSYS','ORDSYS','CTXSYS','MDSYS','WMSYS',
                        'LBACSYS','OLAPSYS','ORDDATA','DVSYS','GSMADMIN_INTERNAL','AUDSYS','GSMCATUSER',
                        'GSMUSER','ANONYMOUS','SI_INFORMTN_SCHEMA','ORDPLUGINS','MDDATA','ORACLE_OCM',
                        'EXFSYS','PUBLIC','REMOTE_SCHEDULER_AGENT','DBSFWUSER','GGSYS','SYSBACKUP',
                        'SYSDG','SYSKM','SYSRAC','DIP','XS$NULL')
              OR owner LIKE 'APEX_%' OR owner LIKE 'FLOWS_%'
         THEN 1 ELSE 0 END
FROM all_views
ORDER BY 1, 2
`

// routinesQuery: procedures + functions. Oracle groups stored code in
// PACKAGE bodies; we list standalone PROCEDURE/FUNCTION objects only
// (object_name IS NOT NULL means not a subprogram inside a package).
const routinesQuery = `
SELECT
    owner        AS schema_name,
    object_name  AS name,
    CASE object_type WHEN 'PROCEDURE' THEN 'P' ELSE 'F' END AS kind,
    'PL/SQL'     AS language,
    CASE WHEN owner IN ('SYS','SYSTEM','OUTLN','XDB','DBSNMP','APPQOSSYS','ORDSYS','CTXSYS','MDSYS','WMSYS',
                        'LBACSYS','OLAPSYS','ORDDATA','DVSYS','GSMADMIN_INTERNAL','AUDSYS','GSMCATUSER',
                        'GSMUSER','ANONYMOUS','SI_INFORMTN_SCHEMA','ORDPLUGINS','MDDATA','ORACLE_OCM',
                        'EXFSYS','PUBLIC','REMOTE_SCHEDULER_AGENT','DBSFWUSER','GGSYS','SYSBACKUP',
                        'SYSDG','SYSKM','SYSRAC','DIP','XS$NULL')
              OR owner LIKE 'APEX_%' OR owner LIKE 'FLOWS_%'
         THEN 1 ELSE 0 END AS is_system
FROM all_objects
WHERE object_type IN ('PROCEDURE', 'FUNCTION')
ORDER BY owner, object_name
`

// triggersQuery: user triggers. trigger_type encodes BEFORE/AFTER +
// row/statement; we split on the leading word.
const triggersQuery = `
SELECT
    owner                                AS schema_name,
    table_name                           AS table_name,
    trigger_name                         AS name,
    CASE WHEN INSTR(trigger_type, 'BEFORE') > 0 THEN 'BEFORE'
         WHEN INSTR(trigger_type, 'AFTER')  > 0 THEN 'AFTER'
         WHEN INSTR(trigger_type, 'INSTEAD') > 0 THEN 'INSTEAD OF'
         ELSE trigger_type
    END                                  AS timing,
    triggering_event                     AS event,
    CASE WHEN owner IN ('SYS','SYSTEM','OUTLN','XDB','DBSNMP','APPQOSSYS','ORDSYS','CTXSYS','MDSYS','WMSYS',
                        'LBACSYS','OLAPSYS','ORDDATA','DVSYS','GSMADMIN_INTERNAL','AUDSYS','GSMCATUSER',
                        'GSMUSER','ANONYMOUS','SI_INFORMTN_SCHEMA','ORDPLUGINS','MDDATA','ORACLE_OCM',
                        'EXFSYS','PUBLIC','REMOTE_SCHEDULER_AGENT','DBSFWUSER','GGSYS','SYSBACKUP',
                        'SYSDG','SYSKM','SYSRAC','DIP','XS$NULL')
              OR owner LIKE 'APEX_%' OR owner LIKE 'FLOWS_%'
         THEN 1 ELSE 0 END AS is_system
FROM all_triggers
ORDER BY owner, table_name, trigger_name
`

// columnsQuery uses :1/:2 (go-ora positional binds).
const columnsQuery = `
SELECT column_name, data_type
FROM all_tab_columns
WHERE owner = :1 AND table_name = :2
ORDER BY column_id
`

// fetchDefinition uses DBMS_METADATA.GET_DDL where available. Returns a
// CLOB that go-ora reads into string. Requires SELECT ANY DICTIONARY or
// ownership — permission errors bubble up normally.
func fetchDefinition(ctx context.Context, sqlDB *sql.DB, kind, schema, name string) (string, error) {
	var objectType string
	switch kind {
	case "view":
		objectType = "VIEW"
	case "procedure":
		objectType = "PROCEDURE"
	case "function":
		objectType = "FUNCTION"
	case "trigger":
		objectType = "TRIGGER"
	default:
		return "", db.ErrDefinitionUnsupported
	}
	q := `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM DUAL`
	var body string
	if err := sqlDB.QueryRowContext(ctx, q, objectType, name, schema).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("no definition for %s %s.%s", kind, schema, name)
		}
		return "", fmt.Errorf("get_ddl: %w", err)
	}
	body = strings.TrimRight(body, "\r\n\t ;")
	if body == "" {
		return "", fmt.Errorf("empty definition for %s %s.%s", kind, schema, name)
	}
	return body + ";", nil
}

// buildDSN produces the oracle:// URL via goora.BuildUrl. cfg.Database is
// the service name; host/port default to localhost:1521.
//
// cfg.Options goes through translateOracleOptions so the sqlgo-side
// snake_case keys become go-ora's space-separated keys on the wire.
func buildDSN(cfg db.Config) string {
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	if port == 0 {
		port = 1521
	}
	return goora.BuildUrl(host, port, cfg.Database, cfg.User, cfg.Password, translateOracleOptions(cfg.Options))
}

// translateOracleOptions converts sqlgo's snake_case option keys to the
// space-separated keys that go-ora's connect_config parser recognizes
// (WALLET, AUTH TYPE, OS USER, SSL, SSL VERIFY, ...). Unknown keys pass
// through verbatim so future go-ora knobs work without another patch.
//
// Supported advanced auth surfaces:
//
//	wallet_path      -> WALLET            (Oracle Wallet directory; holds
//	                                       cwallet.sso / ewallet.p12 and,
//	                                       for TCPS mTLS, the client cert
//	                                       + private key)
//	wallet_password  -> WALLET PASSWORD   (for ewallet.p12)
//	auth_type        -> AUTH TYPE         (OS, KERBEROS, TCPS, or blank)
//	os_user          -> OS USER
//	os_password      -> OS PASS
//	ssl              -> SSL               (true/enable -- required for TCPS)
//	ssl_verify       -> SSL VERIFY        (true/enable; default true)
//	server_dn        -> SSL SERVER CERT DN (match peer DN)
func translateOracleOptions(in map[string]string) map[string]string {
	if len(in) == 0 {
		return in
	}
	semantic := map[string]string{
		"wallet_path":     "WALLET",
		"wallet_password": "WALLET PASSWORD",
		"auth_type":       "AUTH TYPE",
		"os_user":         "OS USER",
		"os_password":     "OS PASS",
		"ssl":             "SSL",
		"ssl_verify":      "SSL VERIFY",
		"server_dn":       "SSL SERVER CERT DN",
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if dst, ok := semantic[strings.ToLower(k)]; ok {
			out[dst] = v
			continue
		}
		out[k] = v
	}
	return out
}
