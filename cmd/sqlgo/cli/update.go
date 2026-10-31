package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Nulifyer/sqlgo/internal/update"
)

type releaseUpdater interface {
	Latest(context.Context) (update.Release, error)
	Install(context.Context, update.Release, string) error
}

func runUpdate(argv []string, stdout, stderr io.Writer) ExitCode {
	return runUpdateWith(argv, stdout, stderr, Version, update.New(), os.Executable)
}

func runUpdateWith(argv []string, stdout, stderr io.Writer, current string, client releaseUpdater, executable func() (string, error)) ExitCode {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "check for a newer stable release without installing")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: sqlgo update [--check]")
		fmt.Fprintln(stderr, "\nUpdate this release binary to the latest stable GitHub release.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return ExitUsage
	}
	if !update.ValidVersion(current) {
		fmt.Fprintln(stderr, "sqlgo update: this is a source or development build. Update with your original build command, or: CGO_ENABLED=1 go install -tags sqlite_fts5 github.com/Nulifyer/sqlgo/cmd/sqlgo@latest")
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	release, err := client.Latest(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sqlgo update: %v\n", err)
		return ExitUsage
	}
	if !update.Newer(release.Version, current) {
		fmt.Fprintf(stdout, "sqlgo %s is up to date (latest stable: %s).\n", current, release.Version)
		return ExitOK
	}
	if *check {
		fmt.Fprintf(stdout, "Update available: %s → %s. Run sqlgo update to install.\n", current, release.Version)
		return ExitOK
	}
	path, err := executable()
	if err != nil {
		fmt.Fprintf(stderr, "sqlgo update: locate executable: %v\n", err)
		return ExitUsage
	}
	fmt.Fprintf(stdout, "Updating sqlgo %s → %s...\n", current, release.Version)
	if err := client.Install(ctx, release, path); err != nil {
		fmt.Fprintf(stderr, "sqlgo update: %v\n", err)
		return ExitUsage
	}
	fmt.Fprintf(stdout, "Installed sqlgo %s.\n", release.Version)
	return ExitOK
}
