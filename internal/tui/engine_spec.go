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
		// Database optional: blank enables the cross-DB explorer tier.
		requiredCore: []int{coreHost, corePort, coreUser, corePassword},
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
		// Postgres cannot cross databases on one connection; DB is required.
		requiredCore: []int{coreHost, corePort, coreUser, corePassword, coreDatabase},
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
		// Database optional: blank enables the cross-DB explorer tier.
		requiredCore: []int{coreHost, corePort, coreUser, corePassword},
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
		fields:      []engineOption{
			// cfg.Database holds the Oracle service name. go-ora accepts
			// extra knobs via cfg.Options (SSL, WALLET, PREFETCH_ROWS...).
		},
		requiredCore: []int{coreHost, corePort, coreUser, corePassword, coreDatabase},
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
}

// aliasLabels gives each alias its display name in the connect form.
var aliasLabels = map[string]string{
	"mariadb":     "MariaDB",
	"cockroachdb": "CockroachDB",
	"supabase":    "Supabase",
	"neon":        "Neon",
	"yugabytedb":  "YugabyteDB",
	"timescaledb": "TimescaleDB",
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
