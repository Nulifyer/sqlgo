package tui

import (
	"github.com/Nulifyer/sqlgo/internal/db/azuresql"
	"github.com/Nulifyer/sqlgo/internal/db/mssql"
)

// engineSpec is the connection-form metadata per driver: default
// port/user + per-engine option fields. Options round-trip through
// config.Connection.Options. Lives in the TUI so the form can
// render without importing driver packages.
//
// Exception: azuresql imports its own FedauthModes slice so the
// fedauth cycler picks up any new modes the driver package adds
// without having to mirror the list here.
type engineSpec struct {
	driver      string
	label       string
	defaultPort int
	defaultUser string
	fields      []engineOption
	// requiredCore lists core* field indices that must be non-empty
	// at save time. Name + Driver are always required and need not
	// be listed. Empty slice = only Name + Driver required (e.g.
	// generic fallback for unknown drivers).
	requiredCore []int
}

// engineOption describes one extra field shown below the core
// block. Non-empty values makes it a cycler (Left/Right steps,
// typing swallowed) constrained to that set. First entry should
// be "" when "driver default" is legitimate.
type engineOption struct {
	key      string
	label    string
	mask     bool
	hint     string // focused help text (future)
	values   []string
	required bool
}

// Enum value sets. Empty string first = "driver default".
var (
	mssqlEncryptValues    = []string{"", "true", "false", "disable", "strict"}
	mssqlBoolValues       = []string{"", "true", "false"}
	postgresSSLModeValues = []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
	mysqlTLSValues        = []string{"", "false", "skip-verify", "preferred", "true"}
	hanaAuthMethodValues  = []string{"", "basic", "jwt", "x509"}
	oracleAuthTypeValues  = []string{"", "OS", "KERBEROS", "TCPS"}
)

// engineSpecs is the registry consulted by the connection form.
// Drivers not listed fall back to a generic no-options spec.
var engineSpecs = []engineSpec{
	{
		driver:      "mssql",
		label:       "MSSQL",
		defaultPort: 1433,
		defaultUser: "sa",
		fields: []engineOption{
			// authenticator selects the integrated-auth provider. Blank =
			// plain SQL auth over TDS. "winsspi" = current Windows identity
			// (or DOMAIN\user + password); Windows-only, the blank import
			// lives in winsspi_windows.go. "ntlm" and "krb5" are cross-
			// platform. krb5-* fields below feed the Kerberos mode.
			{key: "authenticator", label: "Authenticator", values: mssql.AuthModes},
			{key: "encrypt", label: "Encrypt", values: mssqlEncryptValues},
			{key: "TrustServerCertificate", label: "TrustServerCert", values: mssqlBoolValues},
			{key: "app name", label: "App name"},
			// Kerberos knobs. configfile + realm are required for password
			// or keytab mode; keytabfile picks the keytab path; credcachefile
			// points at a ccache for ticket-cache-based login (no password).
			{key: "krb5-configfile", label: "Krb5 config"},
			{key: "krb5-keytabfile", label: "Krb5 keytab"},
			{key: "krb5-credcachefile", label: "Krb5 cred cache"},
			{key: "krb5-realm", label: "Krb5 realm"},
		},
		// User/Password are authenticator-dependent: winsspi w/o User uses
		// the current Windows identity, and krb5 credcache mode needs
		// neither. The driver surfaces clear errors at connect time for
		// missing fields per mode, so they stay out of requiredCore.
		// Database optional: blank enables the cross-DB explorer tier.
		requiredCore: []int{coreHost, corePort},
	},
	{
		driver:      "azuresql",
		label:       "Azure SQL",
		defaultPort: 1433,
		defaultUser: "",
		fields: []engineOption{
			// fedauth picks the AAD/Entra auth mode. Blank = plain SQL auth
			// via the sqlserver driver -- valid for Azure SQL contained
			// database users. Semantic fields below map onto the
			// mode-specific DSN keys in internal/db/azuresql/buildDSN.
			{key: "fedauth", label: "Fedauth", values: azuresql.FedauthModes},
			// Tenant id is the AAD tenant (UUID or domain). Composed with
			// cfg.User into `<client_id>@<tenant_id>` for Service Principal
			// modes. Ignored in the other modes.
			{key: "tenant_id", label: "Tenant ID"},
			// Certificate-based SP auth: path to a .pfx/.pem on disk.
			// cert_password unlocks the cert file; falls back to the core
			// Password field when blank (so users can supply it once).
			{key: "cert_path", label: "Cert path"},
			{key: "cert_password", label: "Cert password", mask: true},
			// Encrypt defaults to true inside buildDSN (Azure SQL mandates
			// TLS); cycler exposes "strict" for mutual-TLS scenarios.
			{key: "encrypt", label: "Encrypt", values: mssqlEncryptValues},
			{key: "app name", label: "App name"},
		},
		// User/Password are mode-dependent (MI/Default need neither,
		// Interactive is optional, Password/SP need both) so they're
		// not in requiredCore -- the driver surfaces a clear error at
		// connect time. Database optional: cross-DB still works server-
		// wide when the login has master access.
		requiredCore: []int{coreHost, corePort},
	},
	{
		driver:      "postgres",
		label:       "Postgres",
		defaultPort: 5432,
		defaultUser: "postgres",
		fields: []engineOption{
			// TLS knobs. sslmode picks the TLS level; sslrootcert points at
			// the CA bundle; sslcert/sslkey enable mutual TLS (client cert
			// auth). channel_binding enforces TLS channel binding for SCRAM
			// on PG 11+; "require" blocks downgrade attacks.
			{key: "sslmode", label: "sslmode", values: postgresSSLModeValues},
			{key: "sslrootcert", label: "SSL root cert"},
			{key: "sslcert", label: "SSL client cert"},
			{key: "sslkey", label: "SSL client key"},
			{key: "channel_binding", label: "Channel binding",
				values: []string{"", "disable", "prefer", "require"}},
			// Kerberos / GSSAPI. gsslib=gssapi forces the GSS handshake;
			// krbsrvname overrides the server principal's service portion
			// (defaults to "postgres"). Unset lets pgx/libpq negotiate.
			{key: "gsslib", label: "GSS lib", values: []string{"", "gssapi", "sspi"}},
			{key: "krbsrvname", label: "Krb5 srv name"},
			// pg_service.conf entry. When set, pgx reads host/port/user/
			// dbname/sslmode from the named service block in the service
			// file (PGSERVICEFILE env or ~/.pg_service.conf).
			{key: "service", label: "Service name"},
			{key: "application_name", label: "App name"},
			// AWS RDS IAM auth. When true, preset.Open generates a fresh
			// 15-min IAM auth token via the AWS SDK and substitutes it
			// for cfg.Password before the DSN is built. Needs aws_region
			// (or default credentials region); sslmode!=disable is also
			// required by RDS IAM on the server side.
			{key: "aws_rds_iam", label: "AWS RDS IAM auth", values: mssqlBoolValues},
			{key: "aws_region", label: "AWS region"},
		},
		// Postgres cannot cross databases on one connection; DB is required.
		// Password is optional because RDS IAM auth or a pg_service file
		// entry can supply it implicitly; the driver surfaces a clear error
		// at connect time when no auth path resolves.
		requiredCore: []int{coreHost, corePort, coreUser, coreDatabase},
	},
	{
		driver:      "mysql",
		label:       "MySQL",
		defaultPort: 3306,
		defaultUser: "root",
		fields: []engineOption{
			{key: "tls", label: "tls", values: mysqlTLSValues},
			{key: "charset", label: "Charset"},
			{key: "collation", label: "Collation"},
			// AWS RDS IAM auth. When true, preset.Open generates a fresh
			// 15-min IAM auth token via the AWS SDK and substitutes it for
			// cfg.Password before the DSN is built. RDS IAM requires the
			// mysql_clear_password plugin (allowCleartextPasswords=true)
			// and TLS on the wire; the driver forces both when the option
			// is truthy. aws_region overrides the SDK-default region.
			{key: "aws_rds_iam", label: "AWS RDS IAM auth", values: mssqlBoolValues},
			{key: "aws_region", label: "AWS region"},
		},
		// Database optional: blank enables the cross-DB explorer tier.
		// Password is optional -- RDS IAM supplies it implicitly.
		requiredCore: []int{coreHost, corePort, coreUser},
	},
	{
		driver:      "sqlite",
		label:       "SQLite",
		defaultPort: 0,
		defaultUser: "",
		fields:      []engineOption{
			// cfg.Database holds the file path; no extra fields needed.
		},
		requiredCore: []int{coreDatabase},
	},
	{
		driver:      "oracle",
		label:       "Oracle",
		defaultPort: 1521,
		defaultUser: "system",
		fields: []engineOption{
			// Auth mode: blank = basic (user + password over TCP). OS +
			// KERBEROS + TCPS are go-ora's AUTH TYPE values. TCPS implies
			// a wallet; OS/KERBEROS run without a password.
			{key: "auth_type", label: "Auth type", values: oracleAuthTypeValues},
			// Oracle Wallet directory. Required for TCPS mTLS (holds the
			// client cert + private key); optional for any mode that wants
			// SSL server-cert validation.
			{key: "wallet_path", label: "Wallet path"},
			{key: "wallet_password", label: "Wallet password", mask: true},
			// SSL flags -- TCPS forces SSL=true; leave blank for plain TCP.
			{key: "ssl", label: "SSL", values: mssqlBoolValues},
			{key: "ssl_verify", label: "SSL verify", values: mssqlBoolValues},
			{key: "server_dn", label: "Server cert DN"},
			// OS auth credentials (AUTH TYPE = OS). Blank falls back to
			// the OS login name; go-ora matches that against SYS.V$SESSION.
			{key: "os_user", label: "OS user"},
			{key: "os_password", label: "OS password", mask: true},
		},
		// Password is only required for basic auth. OS / KERBEROS / TCPS
		// modes supply credentials through AUTH TYPE / wallet / OS login
		// instead, so corePassword is dropped from the required list.
		requiredCore: []int{coreHost, corePort, coreUser, coreDatabase},
	},
	{
		driver:      "firebird",
		label:       "Firebird",
		defaultPort: 3050,
		defaultUser: "sysdba",
		fields: []engineOption{
			{key: "role", label: "Role"},
			{key: "charset", label: "Charset"},
		},
		requiredCore: []int{coreHost, corePort, coreUser, corePassword, coreDatabase},
	},
	{
		driver:      "sybase",
		label:       "Sybase ASE",
		defaultPort: 5000,
		defaultUser: "sa",
		fields:      []engineOption{},
		// Database optional: blank enables the cross-DB explorer tier.
		requiredCore: []int{coreHost, corePort, coreUser, corePassword},
	},
	{
		driver:      "clickhouse",
		label:       "ClickHouse",
		defaultPort: 9000,
		defaultUser: "default",
		fields: []engineOption{
			// secure=true switches the driver to TLS (native port 9440
			// in most deployments). Blank = plaintext TCP on 9000.
			{key: "secure", label: "Secure (TLS)", values: mssqlBoolValues},
			// compress defaults to lz4 in clickhouse-go/v2; expose the
			// common tunable here. Blank = driver default.
			{key: "compress", label: "Compress", values: []string{"", "lz4", "zstd", "none"}},
			{key: "dial_timeout", label: "Dial timeout"},
			// mTLS / custom-TLS surface. Paths live on disk; contents are
			// never copied into the connection record. Setting any of these
			// switches Open to the ParseDSN + OpenDB path so the driver
			// picks up the programmatic *tls.Config.
			{key: "tls_cert_file", label: "Client cert file"},
			{key: "tls_key_file", label: "Client key file"},
			{key: "tls_ca_file", label: "Root CA file"},
			{key: "tls_server_name", label: "TLS server name"},
			{key: "tls_insecure_skip_verify", label: "Skip verify", values: mssqlBoolValues},
		},
		// Database optional: ClickHouse supports `db.table` refs from
		// any session, so the explorer can list every database with no
		// default bound. User defaults to "default" (ClickHouse's
		// built-in passwordless admin).
		requiredCore: []int{coreHost, corePort, coreUser},
	},
	{
		driver:      "vertica",
		label:       "Vertica",
		defaultPort: 5433,
		defaultUser: "dbadmin",
		fields: []engineOption{
			// tlsmode drives TLS handshake. "none" = plaintext (dev);
			// "server" = verify host cert; "server-strict" = also require
			// matching SAN. Setting any of the tls_* file fields below
			// promotes the connection to custom mTLS automatically via
			// vertigo.RegisterTLSConfig -- no need to type "custom" by
			// hand, and password becomes optional.
			{key: "tlsmode", label: "tlsmode",
				values: []string{"", "none", "server", "server-strict", "custom"}},
			// backup_server_node is a comma-separated list of host:port
			// fallbacks the driver cycles through on initial connect.
			{key: "backup_server_node", label: "Backup nodes"},
			{key: "autocommit", label: "Autocommit", values: mssqlBoolValues},
			{key: "use_prepared_stmts", label: "Prepared stmts", values: mssqlBoolValues},
			{key: "client_label", label: "Client label"},
			// mTLS / custom-TLS surface. Paths live on disk; contents are
			// never copied into the connection record.
			{key: "tls_cert_file", label: "Client cert file"},
			{key: "tls_key_file", label: "Client key file"},
			{key: "tls_ca_file", label: "Root CA file"},
			{key: "tls_server_name", label: "TLS server name"},
			{key: "tls_insecure_skip_verify", label: "Skip verify", values: mssqlBoolValues},
		},
		// Vertica connections are pinned to one database. Password is
		// optional -- mTLS / Kerberos / OAuth flows may authenticate
		// without one.
		requiredCore: []int{coreHost, corePort, coreUser, coreDatabase},
	},
	{
		driver:      "trino",
		label:       "Trino / Presto",
		defaultPort: 8080,
		defaultUser: "sqlgo",
		fields: []engineOption{
			// ssl flips the scheme to https and bumps the default port to
			// 8443; password-based basic auth is only honored under https
			// (enforced by the trino-go-client driver).
			{key: "ssl", label: "SSL (https)", values: mssqlBoolValues},
			// schema is the default schema within the catalog -- unqualified
			// table references resolve here. Catalog comes from cfg.Database.
			{key: "schema", label: "Schema"},
			// access_token = JWT for OAuth2-fronted coordinators. Mutually
			// exclusive with password auth in practice.
			{key: "access_token", label: "Access token", mask: true},
			{key: "ssl_cert_path", label: "SSL cert path"},
			{key: "source", label: "Source (client id)"},
		},
		// Database holds the Trino catalog. User required for the
		// X-Trino-User header; blank falls back to "sqlgo" in buildDSN
		// but we still ask for it explicitly to keep audit logs honest.
		requiredCore: []int{coreHost, corePort, coreUser, coreDatabase},
	},
	{
		driver:      "hana",
		label:       "SAP HANA",
		defaultPort: 39017,
		defaultUser: "SYSTEM",
		fields: []engineOption{
			// auth_method routes through the go-hdb Connector API.
			// basic = user+password via DSN; jwt = SAML/JWT bearer
			// (populate jwt_token_file or put the token in the password
			// field); x509 = client certificate (populate client_cert_file
			// + client_key_file).
			{key: "auth_method", label: "Auth method", values: hanaAuthMethodValues},
			{key: "jwt_token_file", label: "JWT token file"},
			{key: "client_cert_file", label: "Client cert file"},
			{key: "client_key_file", label: "Client key file"},
			// default_schema sets the session's unqualified-name resolver.
			// HANA is case-sensitive in quoted identifiers, so upper-case
			// names usually work best.
			{key: "default_schema", label: "Default schema"},
			// TLS knobs. HANA defaults to TLS; tls_insecure_skip_verify=true
			// is dev-only (HXE ships self-signed certs). Real deployments
			// should supply tls_root_ca_file instead.
			{key: "tls_server_name", label: "TLS server name"},
			{key: "tls_insecure_skip_verify", label: "TLS skip verify", values: mssqlBoolValues},
			{key: "tls_root_ca_file", label: "TLS root CA"},
			{key: "locale", label: "Locale"},
			// dfv = data format version. HANA 2.x defaults to 8; override
			// only to match legacy clients.
			{key: "dfv", label: "DFV"},
		},
		// HANA connections pin one tenant DB per connection
		// (SupportsCrossDatabase=false). Password only required for basic
		// auth; JWT/X.509 flows authenticate without it, so the driver
		// surfaces a clear error at connect time if the chosen mode's
		// fields are missing.
		requiredCore: []int{coreHost, corePort, coreUser},
	},
	{
		driver:      "snowflake",
		label:       "Snowflake",
		defaultPort: 0,
		defaultUser: "",
		fields: []engineOption{
			// warehouse names the compute resource. Required for nearly
			// every query path; Snowflake rejects SELECT without one.
			{key: "warehouse", label: "Warehouse"},
			// role overrides the user's default role. Useful when the
			// account grants object access via role switching only.
			{key: "role", label: "Role"},
			// schema seeds the session's default schema so unqualified
			// names resolve there. Composed into the DSN path.
			{key: "schema", label: "Schema"},
			// authenticator toggles the auth flow. Blank = plain user+
			// password. "externalbrowser" opens a browser SSO handshake;
			// "jwt" requires private_key_path below.
			{key: "authenticator", label: "Authenticator",
				values: []string{"", "snowflake", "externalbrowser", "oauth", "jwt", "username_password_mfa"}},
			{key: "private_key_path", label: "Private key path"},
			{key: "private_key_passphrase", label: "Key passphrase", mask: true},
			{key: "application", label: "Application"},
		},
		// Host holds the account identifier (xy12345.us-east-1). Database
		// required because information_schema is DB-scoped. Password is
		// mode-dependent (jwt/externalbrowser don't need it) so it's
		// omitted from requiredCore -- gosnowflake surfaces a clean error
		// at open time when missing.
		requiredCore: []int{coreHost, coreUser, coreDatabase},
	},
	{
		driver:      "databricks",
		label:       "Databricks",
		defaultPort: 443,
		defaultUser: "",
		fields: []engineOption{
			// http_path is required. Looks like /sql/1.0/warehouses/<id>
			// for SQL Warehouses or /sql/1.0/endpoints/<id> for legacy.
			{key: "http_path", label: "HTTP path"},
			// schema seeds the session's default schema; catalog is the
			// core Database field so unqualified names resolve here.
			{key: "schema", label: "Schema"},
			// authType toggles the auth flow. Blank = Pat (personal
			// access token in the Password field). OAuthM2M pulls
			// clientID/clientSecret from Options (or falls back to
			// User/Password). OauthU2M opens a browser SSO handshake.
			{key: "authType", label: "Auth type",
				values: []string{"", "Pat", "OAuthM2M", "OauthU2M"}},
			{key: "clientID", label: "Client ID"},
			{key: "clientSecret", label: "Client secret", mask: true},
			{key: "userAgentEntry", label: "User-agent entry"},
			{key: "timeout", label: "Timeout (s)"},
			{key: "maxRows", label: "Max rows"},
			{key: "useCloudFetch", label: "Cloud fetch", values: mssqlBoolValues},
		},
		// Databricks binds a connection to one catalog (SupportsCrossDatabase
		// false), so require the catalog. Password (PAT) is mode-dependent,
		// omitted from requiredCore -- the driver surfaces a clear error
		// at open time for OauthU2M / misconfigured flows.
		requiredCore: []int{coreHost, coreDatabase},
	},
	{
		driver:      "athena",
		label:       "AWS Athena",
		defaultPort: 0,
		defaultUser: "",
		fields: []engineOption{
			// region is required -- Athena endpoints are region-scoped
			// and the driver's NewConfig rejects empty region.
			{key: "region", label: "Region", required: true},
			// output_location is the s3://bucket/prefix/ base where
			// Athena writes query result CSVs. Required; the driver
			// parses it as the DSN scheme/host/path.
			{key: "output_location", label: "Output location (s3://)", required: true},
			// workgroup scopes query cost limits and output overrides.
			// Defaults to "primary" on the AWS side when unset.
			{key: "workgroup", label: "Workgroup"},
			// aws_profile loads a named profile from ~/.aws/credentials
			// instead of the User/Password static keys. Requires the
			// driver's AWS_SDK_LOAD_CONFIG=1 env flag.
			{key: "aws_profile", label: "AWS profile"},
			// session_token carries STS temporary credentials for
			// assumed-role flows (cfg.User + cfg.Password hold the
			// short-lived access keys). Filled automatically when an
			// assume_role_arn is set; still editable for out-of-band
			// session tokens (e.g. operator pasted from AWS console).
			{key: "session_token", label: "Session token", mask: true},
			// STS pre-flight. When assume_role_arn is set, Open calls
			// sts:AssumeRole (or AssumeRoleWithWebIdentity if a token
			// file is supplied) using the ambient AWS credential chain
			// and overwrites User / Password / session_token with the
			// returned temporary credentials.
			{key: "assume_role_arn", label: "Assume role ARN"},
			{key: "assume_role_session_name", label: "Assume role session name"},
			{key: "assume_role_external_id", label: "Assume role external ID", mask: true},
			{key: "assume_role_duration_seconds", label: "Assume role duration (s)"},
			{key: "web_identity_token_file", label: "Web identity token file"},
			// catalog selects the Glue data catalog (default
			// AwsDataCatalog). Cross-catalog joins are possible but
			// the explorer lists one catalog per connection.
			{key: "catalog", label: "Data catalog"},
			{key: "poll_interval", label: "Poll interval (s)"},
			{key: "read_only", label: "Read-only", values: mssqlBoolValues},
			{key: "moneywise", label: "Money-wise", values: mssqlBoolValues},
		},
		// Athena is credential-flexible -- static keys, named profile,
		// or instance role -- so User/Password aren't in requiredCore.
		// Database holds the Athena/Glue schema name, which is required
		// for unqualified name resolution.
		requiredCore: []int{coreDatabase},
	},
	{
		driver:      "spanner",
		label:       "Cloud Spanner",
		defaultPort: 0,
		defaultUser: "",
		fields: []engineOption{
			// project + instance are required (no default) -- together
			// with cfg.Database they build the projects/.../databases/X
			// DSN path the driver demands.
			{key: "project", label: "Project", required: true},
			{key: "instance", label: "Instance", required: true},
			// autoConfigEmulator=true dials plain-text gRPC AND auto-
			// creates the target instance+database on first Open. Only
			// valid when pointed at the local emulator.
			{key: "autoConfigEmulator", label: "Auto-config emulator", values: mssqlBoolValues},
			// credentials / credentials_json pick the auth path. The
			// driver picks up ADC automatically, so these stay optional
			// and we only surface them for explicit overrides.
			{key: "credentials", label: "Credentials file"},
			{key: "credentials_json", label: "Credentials JSON"},
			// dialect=postgresql switches to a PG-dialect Spanner DB
			// (same query surface, different SQL grammar).
			{key: "dialect", label: "SQL dialect", values: []string{"", "googlesql", "postgresql"}},
			// Session/channel tuning knobs. Mostly dev diagnostics;
			// production picks sensible defaults.
			{key: "num_channels", label: "gRPC channels"},
			{key: "min_sessions", label: "Min sessions"},
			{key: "max_sessions", label: "Max sessions"},
			{key: "database_role", label: "Database role"},
			{key: "use_plain_text", label: "Plain-text gRPC", values: mssqlBoolValues},
		},
		// A Spanner connection is pinned to projects/P/instances/I/
		// databases/D. Host/Port are optional (emulator only). User/
		// Password are unused (auth flows through credentials files or
		// ADC). Database must be set so the DSN path is complete.
		requiredCore: []int{coreDatabase},
	},
	{
		driver:      "bigquery",
		label:       "Google BigQuery",
		defaultPort: 0,
		defaultUser: "",
		fields: []engineOption{
			// project is required. project_id is accepted as an alias by
			// Open but we surface only "project" in the form.
			{key: "project", label: "Project", required: true},
			// dataset mirrors cfg.Database -- either one works. Surface it
			// here so the form can express "set a default dataset" without
			// hijacking the Database field.
			{key: "dataset", label: "Default dataset"},
			// location pins the query region (US, EU, asia-northeast1 ...).
			// Optional; the driver lets BigQuery auto-infer when blank.
			{key: "location", label: "Query location"},
			// credentials / credentials_json pick the auth path. ADC is
			// picked up automatically, so these stay optional.
			{key: "credentials", label: "Credentials file"},
			{key: "credentials_json", label: "Credentials JSON"},
			// access_token is a pre-fetched OAuth bearer token (from
			// `gcloud auth print-access-token` or Workload Identity
			// Federation). No refresh -- reconnect once it expires.
			{key: "access_token", label: "OAuth access token", mask: true},
			// endpoint overrides the API URL -- used for the goccy emulator
			// or a private proxy. Host+Port fill the same role when set.
			{key: "endpoint", label: "API endpoint"},
			// disable_auth skips ADC; required for the goccy emulator
			// unless Host+Port already trigger the implicit skip.
			{key: "disable_auth", label: "Disable auth", values: mssqlBoolValues},
		},
		// A BigQuery connection is pinned to a single project. Host/Port
		// are optional (emulator only). User/Password are unused (auth
		// flows through credentials files or ADC). Database (dataset) is
		// optional -- the explorer works at project-wide scope if unset.
		requiredCore: []int{},
	},
	{
		driver:      "d1",
		label:       "Cloudflare D1",
		defaultPort: 0,
		defaultUser: "",
		fields:      []engineOption{
			// cfg.User = account id, cfg.Database = D1 database id,
			// cfg.Password = API token. Host overrides api.cloudflare.com.
		},
		requiredCore: []int{coreUser, corePassword, coreDatabase},
	},
	{
		driver:      "libsql",
		label:       "libSQL / Turso",
		defaultPort: 0,
		defaultUser: "",
		fields:      []engineOption{
			// cfg.Host holds the Turso database URL; cfg.Password the
			// auth token. No extra fields.
		},
		requiredCore: []int{coreHost, corePassword},
	},
	{
		driver:      "file",
		label:       "File (CSV/TSV/JSONL)",
		defaultPort: 0,
		defaultUser: "",
		fields:      []engineOption{
			// cfg.Database holds a ';'-separated list of file paths.
		},
		requiredCore: []int{coreDatabase},
	},
	{
		driver:       "other",
		label:        "Other...",
		defaultPort:  0,
		defaultUser:  "",
		fields:       []engineOption{},
		requiredCore: []int{},
	},
}

// engineAliases maps a label-only alias to the base driver whose
// engineSpec (fields, defaults) it reuses. Kept in sync with
// internal/db/aliases.
var engineAliases = map[string]string{
	"mariadb":     "mysql",
	"cockroachdb": "postgres",
	"supabase":    "postgres",
	"neon":        "postgres",
	"yugabytedb":  "postgres",
	"timescaledb": "postgres",
	"redshift":    "postgres",
}

// aliasLabels gives each alias its display name in the connect form.
var aliasLabels = map[string]string{
	"mariadb":     "MariaDB",
	"cockroachdb": "CockroachDB",
	"supabase":    "Supabase",
	"neon":        "Neon",
	"yugabytedb":  "YugabyteDB",
	"timescaledb": "TimescaleDB",
	"redshift":    "Amazon Redshift",
}

// engineSpecFor looks up a driver, or returns a generic fallback
// when the driver isn't registered. Aliases reuse the base driver's
// fields/defaults with just the label swapped.
func engineSpecFor(driver string) engineSpec {
	for _, s := range engineSpecs {
		if s.driver == driver {
			return s
		}
	}
	if base, ok := engineAliases[driver]; ok {
		for _, s := range engineSpecs {
			if s.driver == base {
				s.driver = driver
				if lbl, ok := aliasLabels[driver]; ok {
					s.label = lbl
				}
				return s
			}
		}
	}
	return engineSpec{driver: driver, label: driver}
}

// coreLabels provides human-readable names for core* indices used
// in validation error messages.
var coreLabels = [...]string{
	coreName:     "Name",
	coreDriver:   "Driver",
	coreHost:     "Host",
	corePort:     "Port",
	coreUser:     "User",
	corePassword: "Password",
	coreDatabase: "Database",
}

// coreRequired reports whether a core field index must be non-empty
// for this spec. Name and Driver are always required.
func (s engineSpec) coreRequired(idx int) bool {
	if idx == coreName || idx == coreDriver {
		return true
	}
	for _, r := range s.requiredCore {
		if r == idx {
			return true
		}
	}
	return false
}

// driverIndex returns the index of driver in names, or 0 if not found.
func driverIndex(names []string, driver string) int {
	for i, n := range names {
		if n == driver {
			return i
		}
	}
	return 0
}
