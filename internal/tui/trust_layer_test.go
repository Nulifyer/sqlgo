package tui

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/sshtunnel"
	"golang.org/x/crypto/ssh"
)

// testSSHKey builds an ed25519 ssh.PublicKey purely in-memory so
// trust-layer tests don't need network or filesystem keys.
func testSSHKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	return k
}

// TestTrustLayerRejectPopsAndStatus drives the Esc path: the
// overlay should pop itself and surface a reject message on the
// main layer's status line (the picker isn't up in this fixture).
func TestTrustLayerRejectPopsAndStatus(t *testing.T) {
	t.Parallel()
	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}

	err := &sshtunnel.UnknownHostError{
		Host: "bastion.example.com",
		Port: 22,
		Key:  testSSHKey(t),
	}
	tl := newTrustLayer(config.Connection{Name: "prod"}, err)
	a.pushLayer(tl)

	tl.HandleKey(a, Key{Kind: KeyEsc})

	if len(a.layers) != 1 {
		t.Fatalf("layers = %d, want 1 (trust overlay popped)", len(a.layers))
	}
	status := a.mainLayerPtr().status
	if status == "" {
		t.Error("main status empty after reject")
	}
}

// TestTrustLayerAcceptWritesKnownHosts drives the accept path:
// pressing y writes the key to known_hosts. We don't verify the
// reconnect attempt because that would require a live SSH server;
// we only check that the layer committed the trust decision to
// disk via AppendKnownHost.
func TestTrustLayerAcceptWritesKnownHosts(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	restore := sshtunnel.TestOnlySetKnownHostsPath(khPath)
	defer restore()

	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}

	// Accept -> connectTo(target) -> driver.Open. We need the
	// retry to fail fast with something benign (no-such-driver is
	// the cheapest). An empty Driver string hits db.Get(""), which
	// returns an error the connectTo path handles gracefully.
	err := &sshtunnel.UnknownHostError{
		Host: "bastion.example.com",
		Port: 22,
		Key:  testSSHKey(t),
	}
	tl := newTrustLayer(config.Connection{Name: "prod"}, err)
	a.pushLayer(tl)

	tl.HandleKey(a, Key{Kind: KeyRune, Rune: 'y'})

	// After accept the trust overlay should be gone.
	if _, ok := a.topLayer().(*trustLayer); ok {
		t.Errorf("trust overlay still on top after accept")
	}

	// known_hosts should now contain the bastion line.
	data, err2 := os.ReadFile(khPath)
	if err2 != nil {
		t.Fatalf("read known_hosts: %v", err2)
	}
	if len(data) == 0 {
		t.Error("known_hosts file is empty after accept")
	}
}

// TestTrustLayerEnterRequiresConfirmation verifies the two-press
// safety: a single Enter arms but does not accept; the second
// Enter commits.
func TestTrustLayerEnterRequiresConfirmation(t *testing.T) {
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	restore := sshtunnel.TestOnlySetKnownHostsPath(khPath)
	defer restore()

	a := &app{}
	a.layers = []Layer{newMainLayer()}
	a.term = &terminal{width: 80, height: 24}

	err := &sshtunnel.UnknownHostError{
		Host: "bastion.example.com",
		Port: 22,
		Key:  testSSHKey(t),
	}
	tl := newTrustLayer(config.Connection{Name: "prod"}, err)
	a.pushLayer(tl)

	// First Enter: should arm but NOT pop the layer yet.
	tl.HandleKey(a, Key{Kind: KeyEnter})
	if _, ok := a.topLayer().(*trustLayer); !ok {
		t.Fatal("trust overlay popped on first Enter; should require confirmation")
	}
	if !tl.armed {
		t.Fatal("trust overlay not armed after first Enter")
	}

	// Second Enter: commits.
	tl.HandleKey(a, Key{Kind: KeyEnter})
	if _, ok := a.topLayer().(*trustLayer); ok {
		t.Error("trust overlay still on top after confirmed Enter")
	}
}
