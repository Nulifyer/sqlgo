package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func Open(profile ConnectionProfile, registry *Registry) (*sql.DB, Provider, error) {
	return OpenWithSecrets(profile, registry, nil)
}

func OpenWithSecrets(profile ConnectionProfile, registry *Registry, secrets SecretStore) (*sql.DB, Provider, error) {
	provider, ok := registry.Provider(profile.ProviderID)
	if !ok {
		return nil, Provider{}, fmt.Errorf("unknown provider: %s", profile.ProviderID)
	}

	dsn, err := profile.ResolveDSN(secrets)
	if err != nil {
		return nil, Provider{}, err
	}

	conn, err := sql.Open(provider.DriverName, dsn)
	if err != nil {
		return nil, Provider{}, err
	}

	return conn, provider, nil
}

func Ping(ctx context.Context, profile ConnectionProfile, registry *Registry) error {
	return PingWithSecrets(ctx, profile, registry, nil)
}

func PingWithSecrets(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore) error {
	conn, _, err := OpenWithSecrets(profile, registry, secrets)
	if err != nil {
		return err
	}
	defer conn.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return conn.PingContext(pingCtx)
}
