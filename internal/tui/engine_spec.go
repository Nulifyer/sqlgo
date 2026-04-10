package tui

// engineSpec is the connection-form metadata for a single registered
// driver. It covers default port, default username, and the list of
// per-engine option fields that should appear in the form. Options
// round-trip through config.Connection.Options on save; the form
// doesn't need to know their semantics, only their labels and whether
// they should be masked.
//
// Kept here rather than in each db/* package so the TUI can render the
// form without importing any engine adapters beyond the registration
// side-effect imports already in tui.go.
type engineSpec struct {
	driver      string
	label       string
	defaultPort int
	defaultUser string
	fields      []engineOption
}

// engineOption describes one extra field rendered under the core
// Name/Driver/Host/Port/User/Password/Database block.
//
// When values is non-empty the form renders the field as a cycler
// (Left/Right steps through values, printable chars are swallowed)
// instead of a free-form text input. This is how we constrain
// `sslmode`, `encrypt`, `tls`, and other enum-valued options to the
// set their driver actually accepts, turning a cryptic DSN error
// into an impossible-to-type input. The first entry should be the
// empty string when "driver default" is a legitimate choice, so
// the user can still leave the option unset.
type engineOption struct {
	key    string   // map key in Connection.Options
	label  string   // UI label
	mask   bool     // password-like field?
	hint   string   // one-line help text shown when focused (future)
	values []string // non-empty => render as a cycler constrained to these values
}

// Value lists for the constrained enum options. Kept as top-level
// vars so tests can reference them without reaching into the
// registry slice. The empty string is the first entry on every
// cycler so "leave it unset and take the driver default" is still
// the zero-keystroke choice.
var (
	// mssqlEncryptValues mirrors what go-mssqldb parses for the
	// `encrypt` DSN option. "true" and "false" are the modern spelling;
	// "disable" / "strict" match other common DSNs we've seen in the
	// wild.
	mssqlEncryptValues = []string{"", "true", "false", "disable", "strict"}

	// mssqlBoolValues covers TrustServerCertificate and similar
	// boolean-ish MSSQL knobs.
	mssqlBoolValues = []string{"", "true", "false"}

	// postgresSSLModeValues is the full set that libpq and pgx
	// recognize.
	postgresSSLModeValues = []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

	// mysqlTLSValues mirrors go-sql-driver/mysql's `tls` parameter.
	// Custom registered configs ("custom-name") aren't in scope here;
	// users wanting those can still edit the JSON export by hand.
	mysqlTLSValues = []string{"", "false", "skip-verify", "preferred", "true"}
)

// engineSpecs is the registry consulted by the connection form. Drivers
// added here show up in the cycler; drivers not listed (e.g. a
// developer-only adapter) fall back to a generic no-options spec.
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
			// SQLite's "Database" field carries the file path, so no
			// extra fields are needed by default. _pragma is supported
			// by modernc but most users won't need it from the form.
		},
	},
}

// engineSpecFor returns the spec for a driver name, or a generic
// fallback spec when the driver isn't in the registry.
func engineSpecFor(driver string) engineSpec {
	for _, s := range engineSpecs {
		if s.driver == driver {
			return s
		}
	}
	return engineSpec{
		driver:      driver,
		label:       driver,
		defaultPort: 0,
	}
}

// engineSpecIndex returns the index of driver in engineSpecs, or 0 when
// the driver isn't registered. Used by the form's driver cycler.
func engineSpecIndex(driver string) int {
	for i, s := range engineSpecs {
		if s.driver == driver {
			return i
		}
	}
	return 0
}
