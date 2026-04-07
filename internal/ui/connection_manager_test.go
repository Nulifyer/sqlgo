package ui

import (
	"errors"
	"testing"

	"github.com/nulifyer/sqlgo/internal/db"
)

type fakeSecretStore struct {
	values    map[string]string
	setErr    error
	removeErr error
}

func (s fakeSecretStore) Get(key string) (string, error) {
	return s.values[key], nil
}

func (s fakeSecretStore) Set(key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	return nil
}

func (s fakeSecretStore) Remove(key string) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	delete(s.values, key)
	return nil
}

type fakeProfileStore struct {
	profiles      []db.ConnectionProfile
	saveAsErr     error
	replaceAllErr error
	replacedWith  []db.ConnectionProfile
}

func (s *fakeProfileStore) Load() ([]db.ConnectionProfile, error) {
	out := make([]db.ConnectionProfile, len(s.profiles))
	copy(out, s.profiles)
	return out, nil
}

func (s *fakeProfileStore) SaveAs(profile db.ConnectionProfile, previousName string) error {
	if s.saveAsErr != nil {
		return s.saveAsErr
	}

	replaced := false
	for i := range s.profiles {
		if s.profiles[i].Name == profile.Name || (previousName != "" && s.profiles[i].Name == previousName) {
			s.profiles[i] = profile
			replaced = true
			break
		}
	}
	if !replaced {
		s.profiles = append(s.profiles, profile)
	}
	return nil
}

func (s *fakeProfileStore) ReplaceAll(profiles []db.ConnectionProfile) error {
	if s.replaceAllErr != nil {
		return s.replaceAllErr
	}
	s.replacedWith = make([]db.ConnectionProfile, len(profiles))
	copy(s.replacedWith, profiles)
	s.profiles = make([]db.ConnectionProfile, len(profiles))
	copy(s.profiles, profiles)
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

func TestConnectionManagerValidateCurrentStepAllowsAzureADDefaultAuth(t *testing.T) {
	manager := &connectionManager{
		step: connectionStepAuth,
		draft: db.ConnectionProfile{
			ProviderID: db.ProviderAzureSQL,
			AuthMode:   db.AuthAzureAD,
		},
	}

	if err := manager.validateCurrentStep(); err != nil {
		t.Fatalf("validateCurrentStep() error = %v, want nil for Azure AD default auth", err)
	}
}

func TestPersistProfileAndSecretRollsBackProfileOnSecretFailure(t *testing.T) {
	original := db.ConnectionProfile{
		Name:       "old-name",
		ProviderID: db.ProviderSQLite,
		Settings:   db.ConnectionSettings{FilePath: "before.db"},
	}
	store := &fakeProfileStore{
		profiles: []db.ConnectionProfile{original},
	}
	secrets := fakeSecretStore{
		values: map[string]string{},
		setErr: errors.New("keyring write failed"),
	}
	profile := original
	profile.Name = "new-name"
	profile.Settings.FilePath = "after.db"

	err := persistProfileAndSecret(store, secrets, profile, "old-name", "secret")
	if err == nil {
		t.Fatalf("expected persistProfileAndSecret() to fail")
	}

	if len(store.profiles) != 1 {
		t.Fatalf("len(store.profiles) = %d, want 1", len(store.profiles))
	}
	if got := store.profiles[0].Name; got != "old-name" {
		t.Fatalf("store.profiles[0].Name = %q, want old-name", got)
	}
	if got := store.profiles[0].Settings.FilePath; got != "before.db" {
		t.Fatalf("store.profiles[0].Settings.FilePath = %q, want before.db", got)
	}
}
