package db

import "testing"

func TestResolveDSNSQLite(t *testing.T) {
	t.Parallel()

	profile := ConnectionProfile{
		Name:       "dev",
		ProviderID: ProviderSQLite,
		Settings: ConnectionSettings{
			FilePath: `C:\temp\dev.db`,
		},
	}

	dsn, err := profile.ResolveDSN(fakeSecretStore{})
	if err != nil {
		t.Fatalf("ResolveDSN() error = %v", err)
	}
	if dsn != "file:C:/temp/dev.db" {
		t.Fatalf("ResolveDSN() = %q, want %q", dsn, "file:C:/temp/dev.db")
	}
}

func TestResolveDSNPostgresUsesSecretPassword(t *testing.T) {
	t.Parallel()

	profile := ConnectionProfile{
		Name:       "pg-dev",
		ProviderID: ProviderPostgres,
		AuthMode:   AuthUsernamePass,
		Settings: ConnectionSettings{
			Host:        "localhost",
			Port:        5432,
			Database:    "app",
			Username:    "kyle",
			PasswordKey: "profile:pg-dev:password",
		},
	}

	dsn, err := profile.ResolveDSN(fakeSecretStore{
		values: map[string]string{"profile:pg-dev:password": "secret"},
	})
	if err != nil {
		t.Fatalf("ResolveDSN() error = %v", err)
	}

	want := "postgres://kyle:secret@localhost:5432/app?sslmode=disable"
	if dsn != want {
		t.Fatalf("ResolveDSN() = %q, want %q", dsn, want)
	}
}

type fakeSecretStore struct {
	values map[string]string
}

func (s fakeSecretStore) Get(key string) (string, error) {
	return s.values[key], nil
}

func (s fakeSecretStore) Set(key, value string) error {
	return nil
}

func (s fakeSecretStore) Remove(key string) error {
	return nil
}
