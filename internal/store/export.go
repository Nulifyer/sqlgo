package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// ExportJSON writes every saved connection to w as JSON in the same shape
// as the legacy connections.json file. Users can pipe this to a file,
// hand-edit it, and pipe it back through ImportJSON. Keeping the shape
// stable across the store migration means existing connection files
// continue to work as portable config.
func (s *Store) ExportJSON(ctx context.Context, w io.Writer) error {
	conns, err := s.ListConnections(ctx)
	if err != nil {
		return err
	}
	f := config.File{Connections: conns}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&f); err != nil {
		return fmt.Errorf("export connections: %w", err)
	}
	return nil
}

// ImportJSON reads a connections-file shape from r and upserts every
// entry into the store. Existing connections with the same name are
// overwritten. Connections not mentioned in the import file are left
// alone -- this is an upsert, not a replace.
func (s *Store) ImportJSON(ctx context.Context, r io.Reader) (int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("import read: %w", err)
	}
	var f config.File
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("import parse: %w", err)
	}
	for _, c := range f.Connections {
		if err := s.SaveConnection(ctx, "", c); err != nil {
			return 0, fmt.Errorf("import %q: %w", c.Name, err)
		}
	}
	return len(f.Connections), nil
}

// BootstrapFromLegacyConfig is a one-time migration helper. If the store
// has zero saved connections AND a legacy connections.json file exists in
// the config dir, its contents are imported into the store and the source
// file is renamed to connections.json.imported so subsequent boots skip
// this path. Errors are returned but the caller can treat them as
// non-fatal -- an unreadable legacy file should not block startup.
func (s *Store) BootstrapFromLegacyConfig(ctx context.Context) (int, error) {
	existing, err := s.ListConnections(ctx)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}

	legacyPath, err := legacyConnectionsPath()
	if err != nil {
		return 0, err
	}
	f, err := os.Open(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open legacy config: %w", err)
	}
	defer f.Close()

	n, err := s.ImportJSON(ctx, f)
	if err != nil {
		return 0, err
	}

	// Rename the legacy file out of the way so we don't keep importing it.
	imported := legacyPath + ".imported"
	if err := os.Rename(legacyPath, imported); err != nil {
		// Non-fatal: the data is already in the store.
		return n, fmt.Errorf("rename legacy config: %w", err)
	}
	return n, nil
}

// legacyConnectionsPath is the old connections.json location. Matches
// internal/config.Path() but we don't import that function here because
// the config package owns mutation of the file and we only want read +
// rename semantics for the one-shot bootstrap.
func legacyConnectionsPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connections.json"), nil
}
