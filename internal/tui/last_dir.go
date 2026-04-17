package tui

import (
	"context"
	"os"

	"github.com/Nulifyer/sqlgo/internal/store"
)

// seedDir returns the directory a file modal should open at for the
// given kind. It consults the per-cwd last_dirs memory first and falls
// back to the process cwd when no row exists, the store is unavailable,
// or the remembered dir has been deleted since last use. Stale rows are
// left in place so a restored directory is picked up again next run.
func seedDir(a *app, kind store.LastDirKind) string {
	cwd, _ := os.Getwd()
	if a.store == nil {
		return cwd
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeQuickTimeout)
	defer cancel()
	dir, ok, err := a.store.GetLastDir(ctx, cwd, kind)
	if err != nil || !ok || dir == "" {
		return cwd
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return cwd
	}
	return dir
}

// recordDir updates the per-cwd last_dirs memory for the given kind.
// Best-effort: a missing store, empty dir, or write failure is swallowed
// because the memory is an ergonomic hint, not load-bearing state.
func recordDir(a *app, kind store.LastDirKind, dir string) {
	if a.store == nil || dir == "" {
		return
	}
	cwd, _ := os.Getwd()
	if cwd == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeQuickTimeout)
	defer cancel()
	_ = a.store.SetLastDir(ctx, cwd, kind, dir)
}
