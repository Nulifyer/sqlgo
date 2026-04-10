package sshtunnel

// This file wires the SSH tunnel's host key verification into an
// OpenSSH-compatible ~/.ssh/known_hosts database. The default
// callback (see tunnel.go) is now strict: known, matching keys
// proceed silently; unknown hosts error with UnknownHostError so
// the caller can prompt the user for trust-on-first-use; mismatched
// keys error with HostKeyMismatchError, which is unrecoverable
// because it signals either a real MITM or a legitimate but
// silent key rotation that the operator must acknowledge out of
// band.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostError reports that the remote host's key was not in
// the known_hosts database. Callers inspect Host/Port/Key to render
// a fingerprint prompt, and on accept call AppendKnownHost before
// retrying Open. The wrapped stdlib knownhosts error is preserved
// for logging via errors.Unwrap.
type UnknownHostError struct {
	Host string
	Port int
	Key  ssh.PublicKey
	err  error
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("ssh: host %s:%d is not in known_hosts", e.Host, e.Port)
}

func (e *UnknownHostError) Unwrap() error { return e.err }

// HostKeyMismatchError reports that the remote presented a key
// different from the one stored in known_hosts for this host. This
// is treated as fatal -- the caller should NOT offer an override,
// because a mismatch is the exact signal we're trying to catch.
// Operators who legitimately rotated a host key must edit
// known_hosts by hand (or delete the line) and retry.
type HostKeyMismatchError struct {
	Host string
	Port int
	Key  ssh.PublicKey
	err  error
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("ssh: host key for %s:%d does NOT match known_hosts (possible MITM)", e.Host, e.Port)
}

func (e *HostKeyMismatchError) Unwrap() error { return e.err }

// knownHostsPathOverride, when non-empty, is used instead of
// $HOME/.ssh/known_hosts. Tests set it via the TestOnlySet... hook
// below so they don't clobber the real database on the developer's
// machine. Production code leaves it empty.
var knownHostsPathOverride string

// TestOnlySetKnownHostsPath replaces the known_hosts path for the
// duration of a test. Returns a restore function the test should
// defer. Exposed only because the knownhosts plumbing is package-
// private; production code never calls this.
func TestOnlySetKnownHostsPath(path string) (restore func()) {
	prev := knownHostsPathOverride
	knownHostsPathOverride = path
	return func() { knownHostsPathOverride = prev }
}

// defaultKnownHostsPath returns the path sqlgo reads and writes
// ($HOME/.ssh/known_hosts). The containing directory is created
// with 0700 permissions if missing, and an empty known_hosts file
// is touched so knownhosts.New doesn't fail with os.ErrNotExist on
// first run. Any error is reported -- we don't silently swallow
// filesystem problems because that would turn a "can't write the
// database" into a "every host is unknown forever" loop.
func defaultKnownHostsPath() (string, error) {
	var path string
	if knownHostsPathOverride != "" {
		path = knownHostsPathOverride
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home dir: %w", err)
		}
		dir := filepath.Join(home, ".ssh")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		path = filepath.Join(dir, "known_hosts")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return "", fmt.Errorf("create %s: %w", path, err)
		}
		_ = f.Close()
	} else if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	return path, nil
}

// buildHostKeyCallback returns an ssh.HostKeyCallback that checks
// the presented host key against ~/.ssh/known_hosts. Unknown hosts
// return UnknownHostError; mismatches return HostKeyMismatchError.
// Already-trusted hosts return nil so the SSH handshake proceeds.
func buildHostKeyCallback(host string, port int) (ssh.HostKeyCallback, error) {
	path, err := defaultKnownHostsPath()
	if err != nil {
		return nil, err
	}
	inner, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := inner(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keErr *knownhosts.KeyError
		if errors.As(err, &keErr) {
			if len(keErr.Want) == 0 {
				return &UnknownHostError{
					Host: host,
					Port: port,
					Key:  key,
					err:  err,
				}
			}
			return &HostKeyMismatchError{
				Host: host,
				Port: port,
				Key:  key,
				err:  err,
			}
		}
		return err
	}, nil
}

// AppendKnownHost appends a new known_hosts entry for host:port
// with the given public key. Called by the caller after the user
// accepts an UnknownHostError so the next connection to the same
// host proceeds silently. The entry is written in the format the
// stdlib knownhosts.Line helper produces, which matches OpenSSH
// exactly: "[host]:port alg base64key".
//
// Port 22 is emitted without the "[host]:port" wrapper because
// OpenSSH treats that as the implicit default; non-default ports
// require the bracketed form.
func AppendKnownHost(host string, port int, key ssh.PublicKey) error {
	path, err := defaultKnownHostsPath()
	if err != nil {
		return err
	}
	addr := host
	if port != 0 && port != 22 {
		addr = net.JoinHostPort(host, strconv.Itoa(port))
	}
	line := knownhosts.Line([]string{addr}, key)
	// Open for append; create with 0600 if it somehow got removed
	// between defaultKnownHostsPath's touch and now.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}
