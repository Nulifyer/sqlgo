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

	Capabilities Capabilities

	SchemaQuery    string
	ColumnsQuery   string
	ColumnsBuilder func(t TableRef) (string, []any)
	RoutinesQuery  string
	TriggersQuery  string

	IsPermissionDenied func(error) bool

	DefinitionFetcher func(ctx context.Context, db *sql.DB, kind, schema, name string) (string, error)
	ExplainRunner     func(ctx context.Context, db *sql.DB, sql string) ([][]any, error)

	DatabaseListQuery string
	UseDatabaseStmt   func(name string) string

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
		DriverName:         driverName,
		Capabilities:       p.Capabilities,
		SchemaQuery:        p.SchemaQuery,
		ColumnsQuery:       p.ColumnsQuery,
		ColumnsBuilder:     p.ColumnsBuilder,
		RoutinesQuery:      p.RoutinesQuery,
		TriggersQuery:      p.TriggersQuery,
		IsPermissionDenied: p.IsPermissionDenied,
		DefinitionFetcher:  p.DefinitionFetcher,
		ExplainRunner:      p.ExplainRunner,
		DatabaseListQuery:  p.DatabaseListQuery,
		UseDatabaseStmt:    p.UseDatabaseStmt,
		OnClose:            p.OnClose,
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
	opts.DefaultDatabase = cfg.Database
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
