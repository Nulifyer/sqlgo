package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// Profile is the "brain" half of a database adapter: dialect, schema
// queries, capabilities, definition fetcher. Portable across wire
// transports — the same ASE profile can be driven by a native TDS 5.0
// transport today or a JDBC-bridge transport tomorrow.
//
// Profile is a thin wrapper over sqlOptions minus the DriverName field
// (which belongs to the Transport). Every existing engine already fills
// in an sqlOptions in its Open; that body becomes profile.sqlOptions().
type Profile struct {
	// Name is the dialect identifier shown in the "Other..." picker
	// (e.g. "mssql", "postgres", "sybase"). Matches the preset driver
	// name when a profile is the default for one engine.
	Name string

	// SupportedTransports contains the transport names this profile may be
	// composed with. A nil slice leaves deliberate ad-hoc and test profiles
	// unrestricted; registered production profiles declare their transports.
	SupportedTransports []string

	// PrepareConfig performs profile-specific configuration before a transport
	// opens the connection. Authentication preparation belongs here so preset
	// and profile/transport routes behave identically.
	PrepareConfig func(context.Context, Config) (Config, error)

	Capabilities Capabilities

	SchemaQuery    string
	ColumnsQuery   string
	ColumnsBuilder func(t TableRef) (string, []any)
	RoutinesQuery  string
	TriggersQuery  string

	IsPermissionDenied func(error) bool

	DefinitionFetcher  func(ctx context.Context, db *sql.DB, kind, schema, name string) (string, error)
	ExplainRunner      func(ctx context.Context, db *sql.DB, sql string) ([][]any, error)
	TableDesignFetcher func(ctx context.Context, q SQLQuerier, t TableRef) (TableDesign, error)
	// ExplainConnRunner is the pinned-connection form of ExplainRunner for
	// profiles whose EXPLAIN flow changes session state. Cross-database
	// ExplainIn calls it after switching the same physical connection.
	ExplainConnRunner func(ctx context.Context, conn *sql.Conn, sql string) ([][]any, error)

	DatabaseListQuery string
	// CurrentDatabaseQuery returns the database selected on the current
	// physical connection. It is required with UseDatabaseStmt so temporary
	// cross-database work can restore the exact prior session state.
	CurrentDatabaseQuery string
	UseDatabaseStmt      func(name string) string

	// OnClose, if set, runs after the underlying *sql.DB is closed.
	// Used by transports backed by ephemeral resources (temp files,
	// extracted archives) that need cleanup after connection shutdown.
	OnClose func() error
}

// sqlOptions lowers a Profile to the existing openSQL input, binding
// the DriverName from a Transport. Kept as a method so Profile stays
// transport-agnostic.
func (p Profile) sqlOptions(driverName string) sqlOptions {
	return sqlOptions{
		DriverName:           driverName,
		Capabilities:         p.Capabilities,
		SchemaQuery:          p.SchemaQuery,
		ColumnsQuery:         p.ColumnsQuery,
		ColumnsBuilder:       p.ColumnsBuilder,
		RoutinesQuery:        p.RoutinesQuery,
		TriggersQuery:        p.TriggersQuery,
		IsPermissionDenied:   p.IsPermissionDenied,
		DefinitionFetcher:    p.DefinitionFetcher,
		ExplainRunner:        p.ExplainRunner,
		ExplainConnRunner:    p.ExplainConnRunner,
		TableDesignFetcher:   p.TableDesignFetcher,
		DatabaseListQuery:    p.DatabaseListQuery,
		CurrentDatabaseQuery: p.CurrentDatabaseQuery,
		UseDatabaseStmt:      p.UseDatabaseStmt,
		OnClose:              p.OnClose,
	}
}

// Transport is the "wire" half: a database/sql driver name plus a DSN
// builder. One Transport can back many dialects (TDS → mssql + sybase;
// ODBC → many).
type Transport struct {
	// Name is the picker identifier ("tds", "pgx", "mysql", "odbc").
	Name string

	// SQLDriverName is the string passed to sql.Open. Usually equals
	// Name but kept separate so a Transport can front a third-party
	// driver registered under a different id.
	SQLDriverName string

	// DefaultPort is prefilled in the connection form when the user
	// picks this transport without a DB preset.
	DefaultPort int

	// BuildDSN turns a Config into the DSN string sql.Open expects.
	// Required unless Open is set.
	BuildDSN func(cfg Config) string

	// Open, if set, takes precedence over BuildDSN and provides a fully
	// opened *sql.DB plus an optional per-connection cleanup fn that
	// runs after the DB is closed. Used by the "file" transport, which
	// preloads data and may spill to a temp file that needs removal.
	Open func(ctx context.Context, cfg Config) (*sql.DB, func() error, error)

	// SupportsTLS is surfaced to the connection form even when the
	// paired Profile doesn't know (generic "Other..." flow).
	SupportsTLS bool
}

// OpenWith composes a Profile and Transport into a live Conn. This is
// what the "Other... → dialect + driver" flow calls. Preset drivers
// (the existing one-struct-per-engine) will also migrate to this to
// kill their duplicated Open bodies.
func OpenWith(ctx context.Context, p Profile, t Transport, cfg Config) (Conn, error) {
	if err := ValidateProfileTransport(p, t); err != nil {
		return nil, err
	}
	if p.PrepareConfig != nil {
		prepared, err := p.PrepareConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s profile prepare config: %w", p.Name, err)
		}
		cfg = prepared
	}
	var (
		sqlDB   *sql.DB
		cleanup func() error
		err     error
	)
	switch {
	case t.Open != nil:
		sqlDB, cleanup, err = t.Open(ctx, cfg)
	case t.BuildDSN != nil:
		sqlDB, err = sql.Open(t.SQLDriverName, t.BuildDSN(cfg))
	default:
		return nil, fmt.Errorf("db: transport %q has neither Open nor BuildDSN", t.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s/%s open: %w", p.Name, t.Name, err)
	}
	opts := p.sqlOptions(t.SQLDriverName)
	if cleanup != nil {
		prev := opts.OnClose
		opts.OnClose = func() error {
			var cerr error
			if prev != nil {
				cerr = prev()
			}
			if rerr := cleanup(); rerr != nil && cerr == nil {
				cerr = rerr
			}
			return cerr
		}
	}
	return openSQL(ctx, sqlDB, opts)
}

// SupportsTransport reports whether p may be composed with transportName.
// A nil allow-list intentionally keeps unregistered test and ad-hoc profiles
// compatible with arbitrary transports.
func (p Profile) SupportsTransport(transportName string) bool {
	if p.SupportedTransports == nil {
		return true
	}
	for _, name := range p.SupportedTransports {
		if name == transportName {
			return true
		}
	}
	return false
}

// ValidateProfileTransport rejects unsupported profile/transport pairs before
// authentication preparation, tunnel setup, or network work begins.
func ValidateProfileTransport(p Profile, t Transport) error {
	if !p.SupportsTransport(t.Name) {
		return fmt.Errorf("db: profile %q does not support transport %q", p.Name, t.Name)
	}
	return nil
}

// --- profile/transport registries ------------------------------------------
//
// Separate from the Driver registry so the "Other..." picker can list
// dialects and transports independently. Preset drivers are still
// registered via Register(); they'll be rebuilt on top of these once
// every engine is converted.

var (
	profiles   = map[string]Profile{}
	transports = map[string]Transport{}
)

func RegisterProfile(p Profile) {
	regMu.Lock()
	defer regMu.Unlock()
	if p.Name == "" {
		panic("db.RegisterProfile: empty name")
	}
	if _, dup := profiles[p.Name]; dup {
		panic(fmt.Sprintf("db.RegisterProfile: duplicate %q", p.Name))
	}
	profiles[p.Name] = p
}

func RegisterTransport(t Transport) {
	regMu.Lock()
	defer regMu.Unlock()
	if t.Name == "" {
		panic("db.RegisterTransport: empty name")
	}
	if _, dup := transports[t.Name]; dup {
		panic(fmt.Sprintf("db.RegisterTransport: duplicate %q", t.Name))
	}
	transports[t.Name] = t
}

func GetProfile(name string) (Profile, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := profiles[name]
	return p, ok
}

func GetTransport(name string) (Transport, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	t, ok := transports[name]
	return t, ok
}

func RegisteredProfiles() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func RegisteredTransports() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(transports))
	for n := range transports {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RegisteredTransportsForProfile returns registered transport names that the
// named profile supports. Unknown profiles return an error.
func RegisteredTransportsForProfile(profileName string) ([]string, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("db: profile %q not registered", profileName)
	}
	names := make([]string, 0, len(transports))
	for name := range transports {
		if p.SupportsTransport(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
