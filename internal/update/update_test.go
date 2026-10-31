package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStableVersions(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.10.0", "v1.9.9", true}, {"v2.0.0", "v1.99.99", true},
		{"v1.2.4", "v1.2.3", true}, {"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v2.0.0", false}, {"v1.2.4-rc.1", "v1.2.3", false},
		{"v1.2.4", "dev", false}, {"v01.2.4", "v1.2.3", false},
		{"v1.2.4+test", "v1.2.3", false}, {"v1.2", "v1.1.0", false},
		{"v18446744073709551616.0.0", "v1.0.0", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.latest, tc.current, got)
		}
	}
}

func archiveFixture(t *testing.T, goos, name string, kind byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if kind == tar.TypeSymlink {
			h.SetMode(os.ModeSymlink | 0o777)
		} else {
			h.SetMode(0o755)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("new executable"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		h := &tar.Header{Name: name, Mode: 0o755, Typeflag: kind}
		if kind == tar.TypeReg {
			h.Size = int64(len("new executable"))
		} else {
			h.Linkname = "../../outside"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			_, _ = tw.Write([]byte("new executable"))
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func fixtureServer(t *testing.T, goos string, archive []byte, checksum string) (*Client, *atomic.Int32) {
	t.Helper()
	name := "sqlgo_1.10.0_" + goos + "_amd64.tar.gz"
	if goos == "windows" {
		name = "sqlgo_1.10.0_windows_amd64.zip"
	}
	if checksum == "" {
		checksum = fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), name)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.10.0", "assets": []map[string]string{{"name": name}, {"name": "checksums.txt"}}})
		case "/v1.10.0/checksums.txt":
			_, _ = w.Write([]byte(checksum))
		case "/v1.10.0/" + name:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &Client{http: server.Client(), apiURL: server.URL + "/latest", downloadURL: server.URL, goos: goos, goarch: "amd64"}, &calls
}

func TestInstallVerifiedArchive(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			name := "sqlgo"
			if goos == "windows" {
				name += ".exe"
			}
			c, _ := fixtureServer(t, goos, archiveFixture(t, goos, name, tar.TypeReg), "")
			r, err := c.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(target, []byte("old executable"), 0o751); err != nil {
				t.Fatal(err)
			}
			if err := c.Install(context.Background(), r, target); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(target)
			if string(got) != "new executable" {
				t.Fatalf("installed %q", got)
			}
			info, _ := os.Stat(target)
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o751 {
				t.Fatalf("mode = %v", info.Mode())
			}
			assertNoStaging(t, target)
		})
	}
}

func assertNoStaging(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sqlgo-update-") || strings.HasSuffix(entry.Name(), ".update-lock") {
			t.Fatalf("update left %s behind", entry.Name())
		}
	}
}

func TestInstallFailuresPreserveExecutable(t *testing.T) {
	for _, tc := range []struct {
		name, entry, checksum string
		kind                  byte
		goos                  string
	}{
		{"checksum mismatch", "sqlgo", strings.Repeat("0", 64) + "  sqlgo_1.10.0_linux_amd64.tar.gz\n", tar.TypeReg, "linux"},
		{"missing checksum", "sqlgo", "no matching checksum\n", tar.TypeReg, "linux"},
		{"invalid checksum", "sqlgo", "xyz  sqlgo_1.10.0_linux_amd64.tar.gz\n", tar.TypeReg, "linux"},
		{"missing binary", "README", "", tar.TypeReg, "linux"},
		{"traversal", "../../sqlgo", "", tar.TypeReg, "linux"},
		{"tar symlink", "sqlgo", "", tar.TypeSymlink, "linux"},
		{"tar hardlink", "sqlgo", "", tar.TypeLink, "linux"},
		{"zip symlink", "sqlgo.exe", "", tar.TypeSymlink, "windows"},
		{"zip traversal", "../sqlgo.exe", "", tar.TypeReg, "windows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fixtureServer(t, tc.goos, archiveFixture(t, tc.goos, tc.entry, tc.kind), tc.checksum)
			r, err := c.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "sqlgo")
			if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := c.Install(context.Background(), r, target); err == nil {
				t.Fatal("expected rejection")
			}
			got, _ := os.ReadFile(target)
			if string(got) != "old executable" {
				t.Fatalf("old binary changed: %q", got)
			}
			assertNoStaging(t, target)
		})
	}
}

func TestInstallFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	c, _ := fixtureServer(t, "linux", archiveFixture(t, "linux", "sqlgo", tar.TypeReg), "")
	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "actual")
	link := filepath.Join(t.TempDir(), "sqlgo")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(context.Background(), r, link); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(link)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new executable" {
		t.Fatalf("target = %q", got)
	}
}

func TestInstallRunningExecutable(t *testing.T) {
	if os.Getenv("SQLGO_UPDATE_TEST_CHILD") == "1" {
		goos := runtime.GOOS
		name := "sqlgo"
		if goos == "windows" {
			name += ".exe"
		}
		c, _ := fixtureServer(t, goos, archiveFixture(t, goos, name, tar.TypeReg), "")
		r, err := c.Latest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		path, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Install(context.Background(), r, path); err != nil {
			t.Fatal(err)
		}
		return
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "sqlgo.test")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, "-test.run=^TestInstallRunningExecutable$", "-test.timeout=15s")
	cmd.Env = append(os.Environ(), "SQLGO_UPDATE_TEST_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self replacement: %v\n%s", err, out)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new executable" {
		t.Fatalf("running executable was not replaced: %q, %v", got, err)
	}
	assertNoStaging(t, target)
}

func TestCorruptArchivePreservesExecutable(t *testing.T) {
	c, _ := fixtureServer(t, "linux", []byte("not a gzip archive"), "")
	r, _ := c.Latest(context.Background())
	target := filepath.Join(t.TempDir(), "sqlgo")
	_ = os.WriteFile(target, []byte("old executable"), 0o755)
	if err := c.Install(context.Background(), r, target); err == nil || !strings.Contains(err.Error(), "extract binary") {
		t.Fatalf("extraction error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old executable" {
		t.Fatal("corrupt archive changed executable")
	}
	assertNoStaging(t, target)
}

func TestManagedInstallationRefused(t *testing.T) {
	target := filepath.Join(t.TempDir(), "Cellar", "sqlgo", "sqlgo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Executable(target); err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("managed install error = %v", err)
	}
}

func TestInstallRefusesConcurrentUpdate(t *testing.T) {
	c, calls := fixtureServer(t, "linux", archiveFixture(t, "linux", "sqlgo", tar.TypeReg), "")
	r, _ := c.Latest(context.Background())
	target := filepath.Join(t.TempDir(), "sqlgo")
	_ = os.WriteFile(target, []byte("old"), 0o755)
	_ = os.WriteFile(target+".update-lock", nil, 0o600)
	if err := c.Install(context.Background(), r, target); err == nil {
		t.Fatal("accepted concurrent update")
	}
	if calls.Load() != 1 {
		t.Fatal("downloaded before acquiring lock")
	}
	if _, err := os.Stat(target + ".update-lock"); err != nil {
		t.Fatal("removed another updater's lock")
	}
}

func TestLatestRejectsInvalidReleasesAndHTTPFailures(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v1.2.3","prerelease":true}`, `{"tag_name":"v1.2.3","draft":true}`,
		`{"tag_name":"../../bad"}`, `not json`,
		`{"tag_name":"v1.2.3","assets":[{"name":"a"},{"name":"a"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			c := New()
			c.apiURL = s.URL
			if _, err := c.Latest(context.Background()); err == nil {
				t.Fatal("accepted invalid release")
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer s.Close()
	c := New()
	c.apiURL = s.URL
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("HTTP error = %v", err)
	}
}

func TestArchiveSelection(t *testing.T) {
	for _, platform := range []struct{ os, arch, name string }{
		{"linux", "amd64", "sqlgo_1.2.3_linux_amd64.tar.gz"}, {"linux", "arm64", "sqlgo_1.2.3_linux_arm64.tar.gz"},
		{"darwin", "amd64", "sqlgo_1.2.3_darwin_amd64.tar.gz"}, {"darwin", "arm64", "sqlgo_1.2.3_darwin_arm64.tar.gz"},
		{"windows", "amd64", "sqlgo_1.2.3_windows_amd64.zip"}, {"windows", "arm64", ""}, {"freebsd", "amd64", ""},
	} {
		c := &Client{goos: platform.os, goarch: platform.arch}
		r := Release{Version: "v1.2.3", assets: map[string]bool{platform.name: true, "checksums.txt": true}}
		got, err := c.archiveName(r)
		if platform.name == "" {
			if err == nil {
				t.Fatal("accepted unsupported target")
			}
			continue
		}
		if err != nil || got != platform.name {
			t.Fatalf("archive = %q, %v", got, err)
		}
		delete(r.assets, "checksums.txt")
		if _, err := c.archiveName(r); err == nil {
			t.Fatal("accepted missing checksums")
		}
	}
}

func TestWindowsReplacementRollback(t *testing.T) {
	for _, fail := range []string{"", "backup", "install", "rollback", "remove"} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "sqlgo.exe")
			staged := filepath.Join(dir, "new.exe")
			_ = os.WriteFile(target, []byte("old"), 0o755)
			_ = os.WriteFile(staged, []byte("new"), 0o755)
			rename := func(src, dst string) error {
				if (fail == "backup" && src == target) || ((fail == "install" || fail == "rollback") && src == staged) || (fail == "rollback" && src == target+".old") {
					return errors.New("injected rename failure")
				}
				return os.Rename(src, dst)
			}
			remove := func(path string) error {
				if fail == "remove" {
					return errors.New("injected locked backup")
				}
				return os.Remove(path)
			}
			err := replaceWindows(staged, target, rename, remove)
			if (err != nil) != (fail != "") {
				t.Fatalf("error = %v", err)
			}
			if fail == "rollback" {
				got, _ := os.ReadFile(target + ".old")
				if string(got) != "old" || !strings.Contains(err.Error(), "manually") {
					t.Fatalf("backup lost: %q, %v", got, err)
				}
				return
			}
			want := "old"
			if fail == "" {
				want = "new"
			}
			got, _ := os.ReadFile(target)
			if string(got) != want {
				t.Fatalf("target = %q, want %q", got, want)
			}
		})
	}
}

func TestCachedChecks(t *testing.T) {
	c, calls := fixtureServer(t, "linux", nil, "")
	path := filepath.Join(t.TempDir(), "cache", "update.json")
	now := time.Now().UTC()
	if got := c.CachedLatest(context.Background(), "v1.9.0", path, now); got != "v1.10.0" {
		t.Fatalf("latest = %q", got)
	}
	if got := c.CachedLatest(context.Background(), "v1.9.0", path, now.Add(23*time.Hour)); got != "v1.10.0" {
		t.Fatalf("cached = %q", got)
	}
	if got := c.CachedLatest(context.Background(), "v1.10.0", path, now); got != "" {
		t.Fatalf("outdated notice = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d", calls.Load())
	}
	_ = c.CachedLatest(context.Background(), "v1.9.0", path, now.Add(24*time.Hour))
	if calls.Load() != 2 {
		t.Fatalf("did not refresh expired cache: %d", calls.Load())
	}
	_ = c.CachedLatest(context.Background(), "dev", path, now)
	if calls.Load() != 2 {
		t.Fatal("dev build checked network")
	}
}

func TestFailedChecksAreThrottledAndCancellable(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer s.Close()
	c := New()
	c.apiURL = s.URL
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	if got := c.CachedLatest(ctx, "v1.0.0", path, now); got != "" {
		t.Fatalf("failure notice = %q", got)
	}
	if ctx.Err() == nil {
		t.Fatal("check did not reach deadline")
	}
	if got := c.CachedLatest(context.Background(), "v1.0.0", path, now.Add(time.Hour)); got != "" {
		t.Fatal("failure produced notice")
	}
	if calls.Load() != 1 {
		t.Fatalf("retried failed check %d times", calls.Load())
	}
}

func TestConcurrentCachedChecks(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer s.Close()
	c := New()
	c.apiURL = s.URL
	path := filepath.Join(t.TempDir(), "update.json")
	now := time.Now()
	done := make(chan string, 1)
	go func() { done <- c.CachedLatest(context.Background(), "v1.0.0", path, now) }()
	<-entered
	if got := c.CachedLatest(context.Background(), "v1.0.0", path, now); got != "" {
		t.Fatalf("concurrent result = %q", got)
	}
	close(release)
	if got := <-done; got != "v1.2.3" {
		t.Fatalf("first check = %q", got)
	}
}

func TestDisabledChecks(t *testing.T) {
	for _, v := range []string{"0", "false", "off"} {
		t.Setenv("SQLGO_UPDATE_CHECK", v)
		if ChecksEnabled() {
			t.Fatalf("enabled for %q", v)
		}
	}
	t.Setenv("SQLGO_UPDATE_CHECK", "")
	if !ChecksEnabled() {
		t.Fatal("default checks disabled")
	}
}
