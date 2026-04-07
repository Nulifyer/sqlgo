package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func Open(profile ConnectionProfile, registry *Registry) (*sql.DB, Provider, error) {
	provider, ok := registry.Provider(profile.ProviderID)
	if !ok {
		return nil, Provider{}, fmt.Errorf("unknown provider: %s", profile.ProviderID)
	}

	conn, err := sql.Open(provider.DriverName, profile.DSN)
	if err != nil {
		return nil, Provider{}, err
	}

	return conn, provider, nil
}

func Ping(ctx context.Context, profile ConnectionProfile, registry *Registry) error {
	conn, _, err := Open(profile, registry)
	if err != nil {
		return err
	}
	defer conn.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return conn.PingContext(pingCtx)
}
