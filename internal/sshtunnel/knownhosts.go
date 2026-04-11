package sshtunnel

// Host key verification against ~/.ssh/known_hosts. Strict: known
// matching keys pass silently; unknown hosts raise UnknownHostError
// so the caller can prompt for TOFU; mismatches raise
// HostKeyMismatchError which is unrecoverable (mismatch = the
// exact signal we want to catch).

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

// UnknownHostError: host not in known_hosts. Caller prompts with
// Host/Port/Key, calls AppendKnownHost on accept, retries Open.
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

// HostKeyMismatchError: stored key differs from presented key.
// Fatal -- operator must edit known_hosts by hand.
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

// knownHostsPathOverride is set by tests via TestOnlySetKnownHostsPath.
var knownHostsPathOverride string

// TestOnlySetKnownHostsPath replaces the known_hosts path for a
// test. Returns a restore func to defer.
func TestOnlySetKnownHostsPath(path string) (restore func()) {
	prev := knownHostsPathOverride
	knownHostsPathOverride = path
	return func() { knownHostsPathOverride = prev }
}

// defaultKnownHostsPath resolves the known_hosts path and ensures
// the containing directory + file exist. Errors surface rather
// than silently leaving every host unknown forever.
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

// buildHostKeyCallback wraps knownhosts.New, translating its
// KeyError into UnknownHostError / HostKeyMismatchError.
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
				return &UnknownHostError{Host: host, Port: port, Key: key, err: err}
			}
			return &HostKeyMismatchError{Host: host, Port: port, Key: key, err: err}
		}
		return err
	}, nil
}

// AppendKnownHost appends a new entry in OpenSSH format. Port 22
// uses the bare hostname; other ports get "[host]:port".
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
