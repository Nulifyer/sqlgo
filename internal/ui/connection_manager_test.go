package ui

import "testing"

type fakeSecretStore struct {
	values map[string]string
}

func (s fakeSecretStore) Get(key string) (string, error) {
	return s.values[key], nil
}

func (s fakeSecretStore) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s fakeSecretStore) Remove(key string) error {
	delete(s.values, key)
	return nil
}

func TestMigrateProfileSecretRenamesExistingPassword(t *testing.T) {
	store := fakeSecretStore{
		values: map[string]string{
			secretKeyForProfileName("Old Name"): "secret",
		},
	}

	if err := migrateProfileSecret(store, "Old Name", "New Name", ""); err != nil {
		t.Fatalf("migrateProfileSecret() error = %v", err)
	}

	if got := store.values[secretKeyForProfileName("New Name")]; got != "secret" {
		t.Fatalf("new secret = %q, want secret", got)
	}
	if _, ok := store.values[secretKeyForProfileName("Old Name")]; ok {
		t.Fatalf("old secret key still present after rename")
	}
}

func TestMigrateProfileSecretUsesEnteredPassword(t *testing.T) {
	store := fakeSecretStore{
		values: map[string]string{
			secretKeyForProfileName("Old Name"): "secret",
		},
	}

	if err := migrateProfileSecret(store, "Old Name", "New Name", "fresh"); err != nil {
		t.Fatalf("migrateProfileSecret() error = %v", err)
	}

	if got := store.values[secretKeyForProfileName("New Name")]; got != "fresh" {
		t.Fatalf("new secret = %q, want fresh", got)
	}
	if _, ok := store.values[secretKeyForProfileName("Old Name")]; ok {
		t.Fatalf("old secret key still present after explicit password save")
	}
}
