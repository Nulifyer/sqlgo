// Package config persists sqlgo's user-level configuration: saved database
// connections, stored in ~/.sqlgo/connections.json. The file is written
// atomically (temp file + rename) so a crash can't truncate it.
//
// Passwords are stored in plaintext for now. OS keyring integration is a
// future concern.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Connection mirrors db.Config but carries a display name and driver name.
// Kept in this package (not internal/db) so the UI can import it without
// pulling in every engine adapter.
type Connection struct {
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	User     string            `json:"user"`
	Password string            `json:"password,omitempty"`
	Database string            `json:"database,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

// File is the root of the on-disk config.
type File struct {
	Connections []Connection `json:"connections"`
}

// Dir returns the sqlgo config directory, creating it if missing.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".sqlgo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// Path returns the connections file path (whether or not it exists).
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connections.json"), nil
}

// Load reads the connections file. A missing file is not an error; an
// empty File is returned instead. Malformed JSON is an error.
func Load() (*File, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &f, nil
}

// Save writes the connections file atomically: write to a sibling temp file
// then rename over the target. 0600 mode since the file contains passwords.
func Save(f *File) error {
	p, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, p, err)
	}
	return nil
}
