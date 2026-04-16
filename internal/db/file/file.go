// Package file registers a "file" driver that loads CSV, TSV, and
// JSONL files into an in-memory SQLite database and returns a normal
// db.Conn pointed at it. Each file becomes one table named after the
// filename. Import for side effects.
//
// cfg.Database is a list of file paths separated by ';' (or ','). Any
// sqlite URI params in cfg.Options are appended to the backing DSN.
package file

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Nulifyer/sqlgo/internal/db"
	"github.com/Nulifyer/sqlgo/internal/db/fileimport"
	"github.com/Nulifyer/sqlgo/internal/limits"
	"github.com/Nulifyer/sqlgo/internal/sqltok"
)

const driverName = "file"

// tempPrefix is used for spill-to-disk SQLite files. The pid is baked
// into the name so a startup sweep can distinguish files belonging to
// dead processes (orphaned by crash / SIGKILL) from files held by
// other live sqlgo instances.
const tempPrefix = "sqlgo-file"

func init() {
	db.RegisterProfile(Profile)
	db.RegisterTransport(FileTransport)
	db.Register(preset{})
	go sweepOrphans()
}

// Profile mirrors sqlite. File-backed connections can't run EXPLAIN
// usefully, so the format is SQLiteRows and the TUI handles it.
var Profile = db.Profile{
	Name: driverName,
	Capabilities: db.Capabilities{
		SchemaDepth:          db.SchemaDepthFlat,
		LimitSyntax:          db.LimitSyntaxLimit,
		IdentifierQuote:      '"',
		SupportsCancel:       true,
		SupportsTLS:          false,
		ExplainFormat:        db.ExplainFormatSQLiteRows,
		Dialect:              sqltok.DialectSQLite,
		SupportsTransactions: true,
	},
	SchemaQuery: schemaQuery,
	ColumnsBuilder: func(t db.TableRef) (string, []any) {
		q := "SELECT name, type FROM pragma_table_info('" +
			strings.ReplaceAll(t.Name, "'", "''") + "');"
		return q, nil
	},
}

var FileTransport = db.Transport{
	Name:          "file",
	SQLDriverName: "sqlite3",
	DefaultPort:   0,
	SupportsTLS:   false,
	Open:          openFile,
}

type preset struct{}

func (preset) Name() string                  { return driverName }
func (preset) Capabilities() db.Capabilities { return Profile.Capabilities }
func (preset) Open(ctx context.Context, cfg db.Config) (db.Conn, error) {
	return db.OpenWith(ctx, Profile, FileTransport, cfg)
}

// openFile preloads each path into a sqlite backing store and returns
// the opened *sql.DB plus a cleanup that removes any spill file.
func openFile(ctx context.Context, cfg db.Config) (*sql.DB, func() error, error) {
	paths := splitPaths(cfg.Database)
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("file: no paths in Database")
	}

	dsn, tempPath, err := backingDSN(cfg, paths)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() error { return nil }
	if tempPath != "" {
		cleanup = func() error { return os.Remove(tempPath) }
	}

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, nil, fmt.Errorf("file: open sqlite: %w", err)
	}
	// Pin to a single conn: in-memory needs it to share state, and on
	// disk it keeps the import path single-writer (no SQLITE_BUSY).
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		_ = cleanup()
		return nil, nil, fmt.Errorf("file: ping: %w", err)
	}
	for _, p := range paths {
		if _, err := fileimport.Load(ctx, sqlDB, p); err != nil {
			_ = sqlDB.Close()
			_ = cleanup()
			return nil, nil, fmt.Errorf("file: load %q: %w", p, err)
		}
	}
	return sqlDB, cleanup, nil
}

// backingDSN picks between :memory: and an on-disk temp file based on
// the combined size of the input files. Returns the DSN and, when a
// temp file was created, its path so the caller can delete it on close.
// Files that can't be stat'd (e.g. missing) don't trigger spill -- the
// subsequent fileimport.Load will surface the real error.
func backingDSN(cfg db.Config, paths []string) (string, string, error) {
	var total int64
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		total += st.Size()
	}
	if total <= limits.ByteCap() {
		return sharedMemoryDSN(cfg), "", nil
	}
	pattern := fmt.Sprintf("%s-%d-*.db", tempPrefix, os.Getpid())
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", "", fmt.Errorf("file: create temp db: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	// SQLite will create/overwrite the file on open; remove the empty
	// placeholder so it starts clean.
	_ = os.Remove(path)
	return diskDSN(cfg, path), path, nil
}

// orphanRE matches the pid in tempPrefix-<pid>-<rand>.db.
var orphanRE = regexp.MustCompile(`^` + regexp.QuoteMeta(tempPrefix) + `-(\d+)-`)

// sweepOrphans removes spill files left behind by sqlgo processes that
// are no longer running. Called once at package init in a goroutine so
// it never blocks startup. Best-effort: any error (permission, race
// with another sweeper) is silently skipped.
func sweepOrphans() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := orphanRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil || pid == self {
			continue
		}
		if processAlive(pid) {
			continue
		}
		_ = os.Remove(filepath.Join(os.TempDir(), e.Name()))
	}
}

// processAlive reports whether pid is a running process. Uses
// signal-0 on unix; on windows os.FindProcess opens a real handle and
// returns an error for dead pids, which is the cheapest portable check.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// Handle was opened successfully; assume the process exists.
		// (No syscall.Signal(0) equivalent without x/sys.)
		_ = p.Release()
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}

const schemaQuery = `
SELECT
    'main' AS schema_name,
    name,
    CASE WHEN type = 'view' THEN 1 ELSE 0 END AS is_view,
    CASE WHEN name LIKE 'sqlite_%' THEN 1 ELSE 0 END AS is_system
FROM sqlite_master
WHERE type IN ('table','view')
ORDER BY name;
`

// splitPaths accepts ';' or ',' separators. Trims whitespace and
// drops empties.
func splitPaths(in string) []string {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil
	}
	raw := strings.FieldsFunc(in, func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sharedMemoryDSN builds a per-connection in-memory SQLite DSN. Any
// cfg.Options are passed through as URI query params so advanced
// users can set PRAGMAs via mattn/go-sqlite3's _foreign_keys, _journal_mode,
// _busy_timeout, etc. query params.
func sharedMemoryDSN(cfg db.Config) string {
	if len(cfg.Options) == 0 {
		return ":memory:"
	}
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{Scheme: "file", Opaque: ":memory:", RawQuery: q.Encode()}
	return u.String()
}

// diskDSN builds a sqlite DSN for an on-disk temp file used as the
// spill target when total input exceeds diskSpillBytes. cfg.Options
// pass through the same as sharedMemoryDSN.
func diskDSN(cfg db.Config, path string) string {
	if len(cfg.Options) == 0 {
		return path
	}
	q := url.Values{}
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	u := url.URL{Scheme: "file", Opaque: path, RawQuery: q.Encode()}
	return u.String()
}
