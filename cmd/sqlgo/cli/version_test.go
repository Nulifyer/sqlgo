package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionStringUsesLinkerVersion(t *testing.T) {
	original := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = original })

	want := "sqlgo v1.2.3 " + runtime.GOOS + "/" + runtime.GOARCH
	if got := versionString(); got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestGoReleaserInjectsVersionInEveryLdflagsBlock(t *testing.T) {
	configPath := filepath.Join("..", "..", "..", ".goreleaser.yaml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read %s: %v", configPath, err)
	}

	const versionFlag = "-X github.com/Nulifyer/sqlgo/cmd/sqlgo/cli.Version={{.Tag}}"
	ldflagsBlocks := strings.Count(string(config), "ldflags:")
	versionFlags := strings.Count(string(config), versionFlag)
	if ldflagsBlocks == 0 {
		t.Fatal("GoReleaser config has no ldflags blocks")
	}
	if versionFlags != ldflagsBlocks {
		t.Fatalf("GoReleaser version injections = %d, want one in each of %d ldflags blocks", versionFlags, ldflagsBlocks)
	}
}
