package cli

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Version is overridable at build time via
// `-ldflags "-X github.com/Nulifyer/sqlgo/cmd/sqlgo/cli.Version=v1.2.3"`.
// When empty, runVersion falls back to runtime/debug build info so
// `go install github.com/Nulifyer/sqlgo/cmd/sqlgo@latest` still reports
// a meaningful version (module version + vcs.revision).
var Version = ""

func runVersion(_ []string, stdout io.Writer) ExitCode {
	fmt.Fprintln(stdout, versionString())
	return ExitOK
}

func versionString() string {
	v, rev := resolveVersion()
	if rev != "" {
		return fmt.Sprintf("sqlgo %s (%s) %s/%s", v, rev, runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("sqlgo %s %s/%s", v, runtime.GOOS, runtime.GOARCH)
}

func resolveVersion() (version, revision string) {
	if Version != "" {
		return Version, ""
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev", ""
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		v = "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			revision = s.Value[:7]
			break
		}
	}
	return v, revision
}
