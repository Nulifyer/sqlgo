// Package config owns sqlgo's on-disk locations and the shared
// Connection/SSHTunnel types used by the store and its JSON
// export/import shape. Connections themselves are persisted by
// internal/store in sqlgo.db; this package no longer reads or writes
// connections.json.
//
// Passwords on Connection.Password are the in-memory plaintext shape.
// On disk they are replaced with the secret.Placeholder sentinel when
// an OS keyring backend is available; see internal/secret. Plaintext
// only lands in sqlgo.db when no keyring is reachable, in which case
// the TUI surfaces a one-time warning.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

	// Profile overrides Driver when set, selecting a registered dialect
	// (e.g. "mssql", "sybase") independent of the wire transport. Used
	// by the "Other..." connection flow where the user picks dialect +
	// transport separately. Empty means use the preset named by Driver.
	Profile string `json:"profile,omitempty"`

	// Transport overrides the default wire driver when set (e.g. "tds",
	// "pgx", "odbc"). Paired with Profile in the "Other..." flow.
	// Empty means use the preset named by Driver.
	Transport string `json:"transport,omitempty"`

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

// appName is the per-OS subdirectory name placed under each platform
// data root. Kept lowercase to match XDG conventions on Linux; AppData
// and Application Support tolerate the same casing fine.
const appName = "sqlgo"

// DataDir returns the per-user data directory for sqlgo, creating it
// if missing. Resolution is platform-native and respects XDG on Linux:
//
//	Linux:   $XDG_DATA_HOME/sqlgo  (default ~/.local/share/sqlgo)
//	macOS:   ~/Library/Application Support/sqlgo
//	Windows: %LocalAppData%\sqlgo
//
// Migration of legacy state from ~/.sqlgo/ is handled by the install
// scripts, not at runtime.
func DataDir() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, appName)
	mode := os.FileMode(0o755)
	if runtime.GOOS != "windows" {
		mode = 0o700
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, mode); err != nil {
			return "", fmt.Errorf("chmod %s: %w", dir, err)
		}
	}
	return dir, nil
}

func dataRoot() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("LocalAppData"); v != "" {
			return v, nil
		}
		// UserConfigDir on Windows returns %AppData% (Roaming) which
		// is an acceptable fallback when LocalAppData is unset.
		return os.UserConfigDir()
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return v, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		return filepath.Join(home, ".local", "share"), nil
	}
}
