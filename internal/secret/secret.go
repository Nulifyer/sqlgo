// Package secret is sqlgo's abstraction over OS-level secret storage.
// The default implementation wraps github.com/zalando/go-keyring, which
// uses the Windows Credential Manager, macOS Keychain, and the Secret
// Service / KWallet APIs on Linux. Headless environments (CI, bare SSH)
// typically don't have a backend available, so every call can return
// ErrUnavailable -- callers must fall back to plaintext and warn the
// user.
//
// The "placeholder" constant is what gets written into the store's
// password column when a secret has been moved into the keyring. Its
// literal value is intentionally ugly so a real password could only
// match by accident, not by habit.
package secret

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Placeholder is the sentinel stored in the config row's password field
// when the real secret lives in the OS keyring. Code that resolves a
// connection config to a dial-ready Config MUST check for this value and
// substitute the real secret via Load before handing the config to a
// driver.
const Placeholder = "{sqlgo:keyring}"

// serviceName is the single collection identifier used for every sqlgo
// secret. Using one name keeps the OS-level secret browsers tidy (all
// sqlgo entries grouped under "sqlgo") and makes uninstall-cleanup a
// single "delete service" operation.
const serviceName = "sqlgo"

// ErrUnavailable is returned when no OS keyring backend is reachable.
// Callers should treat this as a soft failure: store the password
// plaintext, surface a warning, and keep going.
var ErrUnavailable = errors.New("secret: keyring backend unavailable")

// Store is the minimal interface sqlgo needs from a secret backend.
// Accounts are free-form strings -- sqlgo keys them by connection
// name, so a rename has to follow up with Delete(old) + Set(new).
type Store interface {
	Set(account, value string) error
	Get(account string) (string, error)
	Delete(account string) error
	// Available reports whether the backend is usable on this host.
	// A false return doesn't have to be permanent; a user could plug
	// in a DBus session later. Callers should re-check after events
	// that might enable the backend.
	Available() bool
}

// System returns the default keyring-backed store. Exposed as a
// constructor rather than a package-level var so tests and the in-memory
// fallback can substitute a different implementation cleanly.
func System() Store { return systemStore{} }

type systemStore struct{}

func (systemStore) Set(account, value string) error {
	if err := keyring.Set(serviceName, account, value); err != nil {
		return mapErr(err)
	}
	return nil
}

func (systemStore) Get(account string) (string, error) {
	v, err := keyring.Get(serviceName, account)
	if err != nil {
		return "", mapErr(err)
	}
	return v, nil
}

func (systemStore) Delete(account string) error {
	if err := keyring.Delete(serviceName, account); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return mapErr(err)
	}
	return nil
}

// Available probes the backend by writing and deleting a sentinel entry
// under a reserved account name. Cheap enough to run on boot; the
// result is cached by the caller.
func (systemStore) Available() bool {
	probe := "__sqlgo_probe__"
	if err := keyring.Set(serviceName, probe, "ok"); err != nil {
		return false
	}
	_ = keyring.Delete(serviceName, probe)
	return true
}

// mapErr translates keyring's backend-specific errors into our
// ErrUnavailable sentinel so TUI callers have a single check.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return err // leave the specific "not found" case alone
	}
	// zalando/go-keyring returns ErrUnsupportedPlatform when no backend
	// is reachable, and various driver errors otherwise. Both collapse
	// to ErrUnavailable for the caller's purposes.
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return ErrUnavailable
	}
	return err
}

// Resolve returns the real password for an account. When stored is the
// Placeholder sentinel, the value is fetched from the supplied store
// (typically System()); otherwise stored is already plaintext and is
// returned as-is. A nil store with a placeholder is an explicit error
// so callers fail loudly rather than silently dialling with the
// sentinel string.
func Resolve(store Store, account, stored string) (string, error) {
	if stored != Placeholder {
		return stored, nil
	}
	if store == nil {
		return "", errors.New("secret: password in keyring but no store available")
	}
	v, err := store.Get(account)
	if err != nil {
		return "", err
	}
	return v, nil
}

// NewMemory returns an in-process secret store. Useful for tests and
// for sqlgo runs in headless environments where no OS keyring is
// reachable. Not persisted across runs.
func NewMemory() Store { return &memoryStore{items: map[string]string{}} }

type memoryStore struct {
	items map[string]string
}

func (m *memoryStore) Set(account, value string) error {
	m.items[account] = value
	return nil
}

func (m *memoryStore) Get(account string) (string, error) {
	v, ok := m.items[account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (m *memoryStore) Delete(account string) error {
	delete(m.items, account)
	return nil
}

func (m *memoryStore) Available() bool { return true }
