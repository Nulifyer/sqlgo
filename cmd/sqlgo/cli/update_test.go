package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/update"
)

type fakeUpdater struct {
	latest           string
	err              error
	installErr       error
	checks, installs int
	path             string
}

func (f *fakeUpdater) Latest(context.Context) (update.Release, error) {
	f.checks++
	return update.Release{Version: f.latest}, f.err
}

func (f *fakeUpdater) Install(_ context.Context, _ update.Release, path string) error {
	f.installs++
	f.path = path
	return f.installErr
}

func TestUpdateCommand(t *testing.T) {
	for _, tc := range []struct {
		name, current, latest string
		args                  []string
		checkErr, installErr  error
		code                  ExitCode
		checks, installs      int
		message               string
	}{
		{name: "install", current: "v1.9.0", latest: "v1.10.0", checks: 1, installs: 1, message: "Installed sqlgo v1.10.0"},
		{name: "check only", current: "v1.9.0", latest: "v1.10.0", args: []string{"--check"}, checks: 1, message: "Update available"},
		{name: "current", current: "v1.10.0", latest: "v1.10.0", checks: 1, message: "up to date"},
		{name: "no downgrade", current: "v2.0.0", latest: "v1.10.0", checks: 1, message: "up to date"},
		{name: "source", current: "", code: ExitUsage, message: "go install"},
		{name: "dev", current: "dev", code: ExitUsage, message: "source or development"},
		{name: "help", args: []string{"--help"}, message: "usage: sqlgo update"},
		{name: "bad flag", current: "v1.0.0", args: []string{"--nope"}, code: ExitUsage, message: "flag provided but not defined"},
		{name: "positional", current: "v1.0.0", args: []string{"extra"}, code: ExitUsage, message: "usage"},
		{name: "network failure", current: "v1.0.0", checkErr: errors.New("HTTP 403"), checks: 1, code: ExitUsage, message: "HTTP 403"},
		{name: "install failure", current: "v1.0.0", latest: "v1.10.0", installErr: errors.New("checksum mismatch"), checks: 1, installs: 1, code: ExitUsage, message: "checksum mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeUpdater{latest: tc.latest, err: tc.checkErr, installErr: tc.installErr}
			var stdout, stderr bytes.Buffer
			located := false
			code := runUpdateWith(tc.args, &stdout, &stderr, tc.current, f, func() (string, error) { located = true; return "/custom/sqlgo", nil })
			if code != tc.code || f.checks != tc.checks || f.installs != tc.installs {
				t.Fatalf("code/checks/installs = %d/%d/%d, stderr = %s", code, f.checks, f.installs, stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), tc.message) {
				t.Fatalf("missing %q: stdout %q stderr %q", tc.message, stdout.String(), stderr.String())
			}
			if located != (tc.installs != 0) {
				t.Fatal("located executable for a non-installing command")
			}
			if f.installs > 0 && f.path != "/custom/sqlgo" {
				t.Fatal("wrong executable path")
			}
			if tc.installErr != nil && strings.Contains(stdout.String(), "Installed sqlgo") {
				t.Fatal("claimed success after failure")
			}
		})
	}
}

func TestUpdateHelpAndCompletion(t *testing.T) {
	if !IsVerb("update") {
		t.Fatal("update not dispatched")
	}
	var stdout, stderr bytes.Buffer
	if code := Dispatch([]string{"help", "update"}, nil, &stdout, &stderr); code != ExitOK {
		t.Fatalf("help exit %d", code)
	}
	if !strings.Contains(stderr.String(), "--check") && !strings.Contains(stderr.String(), "-check") {
		t.Fatalf("help = %q", stderr.String())
	}
	for _, shell := range completionShells {
		stdout.Reset()
		stderr.Reset()
		if code := runCompletion([]string{shell}, &stdout, &stderr); code != ExitOK {
			t.Fatal(code)
		}
		checkFlag := "--check"
		if shell == "fish" {
			checkFlag = "-l check"
		}
		if !strings.Contains(stdout.String(), "update") || !strings.Contains(stdout.String(), checkFlag) {
			t.Fatalf("missing update completion for %s", shell)
		}
	}
}
