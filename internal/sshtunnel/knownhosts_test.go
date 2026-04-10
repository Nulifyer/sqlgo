package sshtunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// newTestKey builds a throwaway ed25519 ssh.PublicKey. ed25519 is
// the cheapest stock key type that satisfies ssh.NewPublicKey, and
// the keys stay purely in-memory -- no filesystem or network.
func newTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap public key: %v", err)
	}
	return sshPub
}

// isolateKnownHosts points the package's known_hosts path at a
// fresh file inside t.TempDir() so the test never touches the
// developer's real database. Returns the path for assertions.
func isolateKnownHosts(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	restore := TestOnlySetKnownHostsPath(path)
	t.Cleanup(restore)
	return path
}

func TestDefaultKnownHostsPathCreatesFile(t *testing.T) {
	path := isolateKnownHosts(t)
	got, err := defaultKnownHostsPath()
	if err != nil {
		t.Fatalf("defaultKnownHostsPath: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("known_hosts not created: %v", err)
	}
}

func TestAppendKnownHostWritesOpenSSHFormat(t *testing.T) {
	path := isolateKnownHosts(t)
	key := newTestKey(t)
	if err := AppendKnownHost("bastion.example.com", 22, key); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "bastion.example.com ") {
		t.Errorf("line = %q, want bastion.example.com prefix", line)
	}
	if !strings.Contains(line, "ssh-ed25519 ") {
		t.Errorf("line = %q, want ssh-ed25519 type tag", line)
	}
}

func TestAppendKnownHostNonDefaultPortWrapsBracket(t *testing.T) {
	path := isolateKnownHosts(t)
	key := newTestKey(t)
	if err := AppendKnownHost("bastion.example.com", 2222, key); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "[bastion.example.com]:2222 ") {
		t.Errorf("line = %q, want [host]:port prefix for non-default port", line)
	}
}

func TestHostKeyCallbackUnknownHostReturnsSentinel(t *testing.T) {
	_ = isolateKnownHosts(t)
	cb, err := buildHostKeyCallback("bastion.example.com", 22)
	if err != nil {
		t.Fatalf("buildHostKeyCallback: %v", err)
	}
	key := newTestKey(t)
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 22}
	err = cb("bastion.example.com:22", remote, key)
	if err == nil {
		t.Fatal("callback returned nil for unknown host; expected UnknownHostError")
	}
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %T (%v), want *UnknownHostError", err, err)
	}
	if unknown.Host != "bastion.example.com" || unknown.Port != 22 {
		t.Errorf("unknown.Host/Port = %s:%d, want bastion.example.com:22", unknown.Host, unknown.Port)
	}
	if unknown.Key == nil {
		t.Error("UnknownHostError.Key is nil")
	}
}

func TestHostKeyCallbackKnownHostAcceptsMatchingKey(t *testing.T) {
	_ = isolateKnownHosts(t)
	key := newTestKey(t)
	if err := AppendKnownHost("bastion.example.com", 22, key); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	cb, err := buildHostKeyCallback("bastion.example.com", 22)
	if err != nil {
		t.Fatalf("buildHostKeyCallback: %v", err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 22}
	if err := cb("bastion.example.com:22", remote, key); err != nil {
		t.Errorf("matching key rejected: %v", err)
	}
}

func TestHostKeyCallbackMismatchReturnsSentinel(t *testing.T) {
	_ = isolateKnownHosts(t)
	trusted := newTestKey(t)
	if err := AppendKnownHost("bastion.example.com", 22, trusted); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}
	cb, err := buildHostKeyCallback("bastion.example.com", 22)
	if err != nil {
		t.Fatalf("buildHostKeyCallback: %v", err)
	}
	// Present a DIFFERENT key from the one we just stored.
	impostor := newTestKey(t)
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 22}
	err = cb("bastion.example.com:22", remote, impostor)
	if err == nil {
		t.Fatal("callback returned nil for mismatched key; expected HostKeyMismatchError")
	}
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %T (%v), want *HostKeyMismatchError", err, err)
	}
}

// TestAppendKnownHostRoundTrip is the belt-and-braces check: after
// trusting a new host, the next callback invocation for that host
// returns nil without touching the package at all.
func TestAppendKnownHostRoundTrip(t *testing.T) {
	_ = isolateKnownHosts(t)
	key := newTestKey(t)

	// First callback: unknown host.
	cb, err := buildHostKeyCallback("bastion.example.com", 2222)
	if err != nil {
		t.Fatalf("buildHostKeyCallback: %v", err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 2222}
	err = cb("bastion.example.com:2222", remote, key)
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("first call err = %T (%v), want UnknownHostError", err, err)
	}

	// User accepts -> AppendKnownHost.
	if err := AppendKnownHost(unknown.Host, unknown.Port, unknown.Key); err != nil {
		t.Fatalf("AppendKnownHost: %v", err)
	}

	// Rebuild the callback (production code does this on every
	// Open), and the same key should now verify cleanly.
	cb2, err := buildHostKeyCallback("bastion.example.com", 2222)
	if err != nil {
		t.Fatalf("buildHostKeyCallback #2: %v", err)
	}
	if err := cb2("bastion.example.com:2222", remote, key); err != nil {
		t.Errorf("post-trust callback rejected the key: %v", err)
	}
}
