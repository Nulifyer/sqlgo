// Package update discovers stable sqlgo releases and installs verified binaries.
// It never opens the connection store or changes application data.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxArchive = 1 << 30

// Client owns release discovery, archive selection, and verified installation.
// New uses only the official Nulifyer/sqlgo GitHub release endpoints.
type Client struct {
	http                *http.Client
	apiURL, downloadURL string
	goos, goarch        string
}

func New() *Client {
	return &Client{
		http:        &http.Client{Timeout: 3 * time.Minute},
		apiURL:      "https://api.github.com/repos/Nulifyer/sqlgo/releases/latest",
		downloadURL: "https://github.com/Nulifyer/sqlgo/releases/download",
		goos:        runtime.GOOS, goarch: runtime.GOARCH,
	}
}

// Release contains a validated stable release tag and its published asset names.
type Release struct {
	Version string
	assets  map[string]bool
}

func versionParts(v string) ([3]uint64, bool) {
	var result [3]uint64
	if !strings.HasPrefix(v, "v") {
		return result, false
	}
	parts := strings.Split(v[1:], ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return result, false
			}
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

// ValidVersion accepts full stable release tags, such as v1.2.3.
func ValidVersion(v string) bool { _, ok := versionParts(v); return ok }

// Newer reports whether latest is a stable release newer than current.
func Newer(latest, current string) bool {
	l, lok := versionParts(latest)
	c, cok := versionParts(current)
	if !lok || !cok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func (c *Client) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sqlgo-updater")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("release request: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	body, err := c.get(ctx, c.apiURL)
	if err != nil {
		return Release{}, err
	}
	defer body.Close()
	var data struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&data); err != nil {
		return Release{}, fmt.Errorf("read release: %w", err)
	}
	if data.Draft || data.Prerelease || !ValidVersion(data.Tag) {
		return Release{}, errors.New("latest release has no valid stable version")
	}
	r := Release{Version: data.Tag, assets: make(map[string]bool)}
	for _, a := range data.Assets {
		if r.assets[a.Name] {
			return Release{}, fmt.Errorf("duplicate release asset %q", a.Name)
		}
		r.assets[a.Name] = true
	}
	return r, nil
}

func (c *Client) archiveName(r Release) (string, error) {
	if !ValidVersion(r.Version) {
		return "", errors.New("invalid release version")
	}
	if (c.goos != "linux" && c.goos != "darwin" && c.goos != "windows") ||
		(c.goarch != "amd64" && c.goarch != "arm64") || (c.goos == "windows" && c.goarch != "amd64") {
		return "", fmt.Errorf("no release binary for %s/%s", c.goos, c.goarch)
	}
	ext := ".tar.gz"
	if c.goos == "windows" {
		ext = ".zip"
	}
	name := fmt.Sprintf("sqlgo_%s_%s_%s%s", strings.TrimPrefix(r.Version, "v"), c.goos, c.goarch, ext)
	if !r.assets[name] || !r.assets["checksums.txt"] {
		return "", fmt.Errorf("release %s is missing %s or checksums.txt", r.Version, name)
	}
	return name, nil
}

func (c *Client) checksum(ctx context.Context, r Release, name string) (string, error) {
	body, err := c.get(ctx, c.downloadURL+"/"+r.Version+"/checksums.txt")
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(io.LimitReader(body, 1<<20))
	expected := ""
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size || expected != "" {
			return "", errors.New("invalid or duplicate archive checksum")
		}
		expected = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if expected == "" {
		return "", fmt.Errorf("no checksum for %s", name)
	}
	return expected, nil
}

// Executable resolves symlinks and refuses known package-manager locations.
// Release-build provenance is checked by the CLI before calling Install.
func Executable(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	normalized := strings.ToLower(filepath.ToSlash(resolved))
	for _, marker := range []string{"/cellar/", "/scoop/apps/", "/windowsapps/", "/nix/store/"} {
		if strings.Contains(normalized, marker) {
			return "", errors.New("this installation is managed externally; update it with its package manager")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return "", errors.New("refusing to replace a non-regular or privileged executable")
	}
	return resolved, nil
}

// Install stages the download beside the resolved executable, verifies the
// archive's SHA-256, then replaces the binary. If Windows rollback also fails,
// the old binary remains at .old and the error explains how to restore it.
func (c *Client) Install(ctx context.Context, r Release, executable string) error {
	name, err := c.archiveName(r)
	if err != nil {
		return err
	}
	target, err := Executable(executable)
	if err != nil {
		return err
	}
	lockPath := target + ".update-lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("cannot lock update destination (check permissions or another update; remove %s only if no update is running): %w", lockPath, err)
	}
	lock.Close()
	defer os.Remove(lockPath)
	stage, err := os.MkdirTemp(filepath.Dir(target), ".sqlgo-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	expected, err := c.checksum(ctx, r, name)
	if err != nil {
		return err
	}
	body, err := c.get(ctx, c.downloadURL+"/"+r.Version+"/"+name)
	if err != nil {
		return err
	}
	defer body.Close()
	archive, err := os.Create(filepath.Join(stage, name))
	if err != nil {
		return err
	}
	defer archive.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(body, maxArchive+1))
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	if n > maxArchive {
		return errors.New("release archive exceeds size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("archive checksum mismatch; installed binary was preserved")
	}
	staged := filepath.Join(stage, "sqlgo")
	if err := extractBinary(archive, staged, c.goos); err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if err := os.Chmod(staged, info.Mode().Perm()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return replaceExecutable(staged, target)
}

func copyBinary(src io.Reader, dest string) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(src, maxArchive+1))
	if err != nil {
		return err
	}
	if n == 0 || n > maxArchive {
		return errors.New("invalid binary size")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

func extractBinary(archive *os.File, dest, goos string) error {
	if goos == "windows" {
		info, err := archive.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(archive, info.Size())
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			if f.Name != "sqlgo.exe" {
				continue
			}
			if !f.Mode().IsRegular() {
				return errors.New("archive binary is not a regular file")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			return copyBinary(r, dest)
		}
	} else {
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			return err
		}
		gz, err := gzip.NewReader(archive)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, maxArchive+1))
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if h.Name != "sqlgo" {
				continue
			}
			if h.Typeflag != tar.TypeReg {
				return errors.New("archive binary is not a regular file")
			}
			return copyBinary(tr, dest)
		}
	}
	return errors.New("release archive does not contain the expected executable")
}
