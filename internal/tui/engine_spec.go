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
type engineOption struct {
	key   string // map key in Connection.Options
	label string // UI label
	mask  bool   // password-like field?
	hint  string // one-line help text shown when focused (future)
}

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
			{key: "encrypt", label: "Encrypt"},
			{key: "TrustServerCertificate", label: "TrustServerCert"},
			{key: "app name", label: "App name"},
		},
	},
	{
		driver:      "postgres",
		label:       "Postgres",
		defaultPort: 5432,
		defaultUser: "postgres",
		fields: []engineOption{
			{key: "sslmode", label: "sslmode"},
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
			{key: "tls", label: "tls"},
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
