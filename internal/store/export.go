package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/secret"
)

// ExportJSON writes every saved connection to w as JSON. Users can pipe
// this to a file, hand-edit it, and pipe it back through ImportJSON.
// Keyring references stay as placeholders. Plaintext passwords and sensitive
// adapter options become RedactedPlaceholder, so the export is config metadata
// and never a portable secret backup.
func (s *Store) ExportJSON(ctx context.Context, w io.Writer) error {
	conns, err := s.ListConnections(ctx)
	if err != nil {
		return err
	}
	for i := range conns {
		conns[i] = redactConnectionForExport(conns[i])
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
// alone. RedactedPlaceholder preserves an existing local secret and becomes
// empty when the imported connection is new.
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
		current, err := s.GetConnection(ctx, c.Name)
		switch {
		case err == nil:
			c = restoreRedactedConnection(c, current)
		case errors.Is(err, ErrConnectionNotFound):
			c = restoreRedactedConnection(c, config.Connection{})
		default:
			return 0, fmt.Errorf("import %q: read current connection: %w", c.Name, err)
		}
		if err := s.SaveConnection(ctx, "", c); err != nil {
			return 0, fmt.Errorf("import %q: %w", c.Name, err)
		}
	}
	return len(f.Connections), nil
}

func redactConnectionForExport(c config.Connection) config.Connection {
	c.Password = secret.RedactStoredValue(c.Password)
	c.SSH.Password = secret.RedactStoredValue(c.SSH.Password)
	c.Options = secret.RedactOptions(c.Options)
	return c
}

func restoreRedactedConnection(imported, current config.Connection) config.Connection {
	imported.Password = secret.RestoreRedactedValue(imported.Password, current.Password)
	imported.SSH.Password = secret.RestoreRedactedValue(imported.SSH.Password, current.SSH.Password)
	imported.Options = secret.RestoreRedactedOptions(imported.Options, current.Options)
	return imported
}
