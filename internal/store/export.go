package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Nulifyer/sqlgo/internal/config"
)

// ExportJSON writes every saved connection to w as JSON. Users can pipe
// this to a file, hand-edit it, and pipe it back through ImportJSON.
// Keyring-backed secrets stay as placeholders, so the export is config
// metadata, not a portable secret backup.
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
