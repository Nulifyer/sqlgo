package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// LastDirKind names a modal whose last-used directory is remembered on
// a per-cwd basis. Values are stored verbatim in the last_dirs.kind
// column, so these constants are part of the on-disk format; do not
// rename without a migration.
type LastDirKind string

const (
	LastDirOpen   LastDirKind = "open"
	LastDirSave   LastDirKind = "save"
	LastDirExport LastDirKind = "export"
)

// GetLastDir returns the remembered directory for (cwd, kind), or ""
// with ok=false when no row has been recorded yet. Both cwd and the
// stored dir are round-tripped through filepath.Abs + filepath.Clean
// so lookups match previous writes regardless of how the caller
// formatted the path. Callers should still stat the returned dir
// before using it; rows outlive directory deletion on purpose.
func (s *Store) GetLastDir(ctx context.Context, cwd string, kind LastDirKind) (string, bool, error) {
	key, err := normalizePath(cwd)
	if err != nil {
		return "", false, err
	}
	var dir string
	err = s.db.QueryRowContext(ctx,
		`SELECT dir FROM last_dirs WHERE cwd = ? AND kind = ?`,
		key, string(kind),
	).Scan(&dir)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get last dir: %w", err)
	}
	return dir, true, nil
}

// SetLastDir upserts the remembered directory for (cwd, kind). Passing
// an empty dir is a no-op error (callers are expected to filter) so
// we never overwrite a good row with a blank. updated_at is a unix
// second timestamp to match the lightweight integer storage we use
// elsewhere in the store; it is informational only.
func (s *Store) SetLastDir(ctx context.Context, cwd string, kind LastDirKind, dir string) error {
	if dir == "" {
		return fmt.Errorf("set last dir: empty dir")
	}
	cwdKey, err := normalizePath(cwd)
	if err != nil {
		return err
	}
	dirVal, err := normalizePath(dir)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO last_dirs (cwd, kind, dir, updated_at)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(cwd, kind) DO UPDATE SET
            dir        = excluded.dir,
            updated_at = excluded.updated_at
    `, cwdKey, string(kind), dirVal, now)
	if err != nil {
		return fmt.Errorf("set last dir: %w", err)
	}
	return nil
}

// normalizePath resolves relative paths against the process cwd and
// cleans separators so two equivalent spellings collapse to the same
// key. Empty input is rejected so we never silently store "" as a
// path, which would bucket all blank writes under the same row.
func normalizePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("abs path %q: %w", p, err)
	}
	return filepath.Clean(abs), nil
}
