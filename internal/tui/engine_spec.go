package tui

// engineSpec is the connection-form metadata per driver: default
// port/user + per-engine option fields. Options round-trip through
// config.Connection.Options. Lives in the TUI so the form can
// render without importing driver packages.
type engineSpec struct {
	driver      string
	label       string
	defaultPort int
	defaultUser string
	fields      []engineOption
}

// engineOption describes one extra field shown below the core
// block. Non-empty values makes it a cycler (Left/Right steps,
// typing swallowed) constrained to that set. First entry should
// be "" when "driver default" is legitimate.
type engineOption struct {
	key    string
	label  string
	mask   bool
	hint   string // focused help text (future)
	values []string
}

// Enum value sets. Empty string first = "driver default".
var (
	mssqlEncryptValues    = []string{"", "true", "false", "disable", "strict"}
	mssqlBoolValues       = []string{"", "true", "false"}
	postgresSSLModeValues = []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
	mysqlTLSValues        = []string{"", "false", "skip-verify", "preferred", "true"}
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
			{key: "encrypt", label: "Encrypt", values: mssqlEncryptValues},
			{key: "TrustServerCertificate", label: "TrustServerCert", values: mssqlBoolValues},
			{key: "app name", label: "App name"},
		},
	},
	{
		driver:      "postgres",
		label:       "Postgres",
		defaultPort: 5432,
		defaultUser: "postgres",
		fields: []engineOption{
			{key: "sslmode", label: "sslmode", values: postgresSSLModeValues},
			{key: "sslrootcert", label: "SSL root cert"},
			{key: "application_name", label: "App name"},
		},
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
		},
	},
	{
		driver:      "sqlite",
		label:       "SQLite",
		defaultPort: 0,
		defaultUser: "",
		fields: []engineOption{
			// cfg.Database holds the file path; no extra fields needed.
		},
	},
}

// engineSpecFor looks up a driver, or returns a generic fallback
// when the driver isn't registered.
func engineSpecFor(driver string) engineSpec {
	for _, s := range engineSpecs {
		if s.driver == driver {
			return s
		}
	}
	return engineSpec{driver: driver, label: driver}
}

// engineSpecIndex returns the index of driver, or 0 if unknown.
func engineSpecIndex(driver string) int {
	for i, s := range engineSpecs {
		if s.driver == driver {
			return i
		}
	}
	return 0
}
