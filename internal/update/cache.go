package update

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ChecksEnabled controls automatic TUI checks only. Explicit CLI checks still
// work when automatic checks are disabled.
func ChecksEnabled() bool {
	switch os.Getenv("SQLGO_UPDATE_CHECK") {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

func CachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sqlgo", "update.json"), nil
}

type cachedRelease struct {
	Checked time.Time `json:"checked_at"`
	Version string    `json:"version,omitempty"`
}

// CachedLatest reuses a daily check, including failed attempts. Cache or network
// errors are silent. Callers run this off the UI loop with a short deadline.
func (c *Client) CachedLatest(ctx context.Context, current, path string, now time.Time) string {
	if !ValidVersion(current) {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	unlock, err := lockCache(path + ".lock")
	if err != nil {
		return ""
	}
	defer unlock()
	var cached cachedRelease
	if f, err := os.Open(path); err == nil {
		_ = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&cached)
		f.Close()
	}
	if cached.Checked.After(now) || now.Sub(cached.Checked) >= 24*time.Hour {
		cached.Checked = now
		// Persist the attempt before networking so repeated failures are throttled.
		if err := saveCache(path, cached); err != nil {
			return ""
		}
		if release, err := c.Latest(ctx); err == nil {
			cached.Version = release.Version
			_ = saveCache(path, cached)
		}
	}
	if Newer(cached.Version, current) {
		return cached.Version
	}
	return ""
}

func saveCache(path string, cached cachedRelease) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
