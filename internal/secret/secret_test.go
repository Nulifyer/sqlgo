package secret

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	if err := s.Set("acct", "hunter2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("acct")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get = %q, want hunter2", got)
	}
	if err := s.Delete("acct"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("acct"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("Get after Delete err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreDeleteMissingIsErrNotFound(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	// Memory store's Delete is idempotent (no error on missing). Get
	// on a missing key must report ErrNotFound.
	if err := s.Delete("never-existed"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
	if _, err := s.Get("never-existed"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("Get missing err = %v, want ErrNotFound", err)
	}
}

func TestPlaceholderIsRecognizable(t *testing.T) {
	t.Parallel()
	// Sanity: the placeholder must be distinct enough that a real
	// password is extremely unlikely to collide with it.
	if Placeholder == "" || Placeholder == "password" {
		t.Errorf("Placeholder %q is not distinctive enough", Placeholder)
	}
}
