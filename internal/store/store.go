// Package store is sqlgo's self-hosted SQLite backing store. Connections
// (Phase 1.6), query history (Phase 1.7), and any future user-level state
// live in a single sqlgo.db file inside the sqlgo config directory. We
// dogfood the pure-Go SQLite driver we ship to users, so the store path
// gives us constant coverage of the read path with zero extra footprint.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// Store is an open handle on sqlgo.db. The underlying *sql.DB is safe for
// concurrent use; we run in WAL mode so reads don't block the occasional
// writer (new history row, edited connection).
type Store struct {
	db *sql.DB

	// historyRingMax is the per-connection history row cap. Stored on
	// the handle (not globally) so tests can configure each store
	// independently without racing.
	historyRingMax int
}

// DefaultHistoryRingMax is the default per-connection history cap used
// by Open/OpenAt. Exposed so callers can reason about the ring size in
// user-facing copy.
const DefaultHistoryRingMax = 1000

// SetHistoryRingMax overrides the per-connection history row cap on this
// store. The change takes effect on the next RecordHistory call. A cap
// of zero or negative is treated as DefaultHistoryRingMax.
func (s *Store) SetHistoryRingMax(n int) {
	if n <= 0 {
		n = DefaultHistoryRingMax
	}
	s.historyRingMax = n
}

// Open opens the default sqlgo store at <config dir>/sqlgo.db, creating
// the file and applying any pending migrations.
func Open(ctx context.Context) (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return OpenAt(ctx, filepath.Join(dir, "sqlgo.db"))
}

// OpenAt opens a store at the given filesystem path. Used by tests to
// point the store at a temp directory.
func OpenAt(ctx context.Context, path string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store ping: %w", err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store migrate: %w", err)
	}
	return &Store{db: sqlDB, historyRingMax: DefaultHistoryRingMax}, nil
}

// DB returns the underlying database handle. Exposed so sibling packages
// (Phase 1.6 connections, Phase 1.7 history) can run their own SQL against
// the same store without re-opening it. Callers must not Close() it.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the store.
func (s *Store) Close() error {
	return s.db.Close()
}

// dsnFor builds a modernc.org/sqlite URI DSN with the pragmas we want on
// every store connection. Forward slashes are used for Windows paths so
// the URI parses cleanly; sqlite accepts either separator on Windows.
func dsnFor(path string) string {
	// file:/C:/Users/... is the portable spelling. ToSlash normalizes
	// backslashes, and the leading slash after "file:" is required so
	// sqlite treats the path as absolute.
	p := filepath.ToSlash(path)
	if len(p) == 0 || p[0] != '/' {
		p = "/" + p
	}
	return "file:" + p +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(wal)" +
		"&_pragma=busy_timeout(5000)"
}
