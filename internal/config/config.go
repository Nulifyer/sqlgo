// Package config owns sqlgo's on-disk config directory and the shared
// Connection/SSHTunnel types used by the store and its JSON export/import
// shape. Connections themselves are persisted by internal/store in
// sqlgo.db; this package no longer reads or writes connections.json.
//
// Passwords are stored in plaintext for now. OS keyring integration is a
// future concern.
package config

import (
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

	// SSH holds the optional jump-host tunnel settings. Zero value
	// means no tunneling; when SSH.Host is non-empty the TUI dials
	// the target database through a local port forwarded over SSH.
	// Kept as a nested struct so the JSON shape stays tidy.
	SSH SSHTunnel `json:"ssh,omitempty"`
}

// SSHTunnel describes an SSH jump host used to reach the database.
// Exactly one of Password or KeyPath authenticates the SSH connection;
// KeyPath takes precedence when both are set. Empty Host means no tunnel.
type SSHTunnel struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
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

