package db

import (
	"context"
	"fmt"
)

type ExplorerObjectType string

const (
	ExplorerDatabase ExplorerObjectType = "database"
	ExplorerTable    ExplorerObjectType = "table"
	ExplorerView     ExplorerObjectType = "view"
)

type ExplorerObject struct {
	Type        ExplorerObjectType
	Name        string
	Qualified   string
	Description string
}

type ExplorerSnapshot struct {
	Databases []ExplorerObject
	Tables    []ExplorerObject
	Views     []ExplorerObject
}

func LoadExplorerSnapshot(ctx context.Context, profile ConnectionProfile, registry *Registry) (ExplorerSnapshot, error) {
	return LoadExplorerSnapshotWithSecrets(ctx, profile, registry, nil)
}

func LoadExplorerSnapshotWithSecrets(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore) (ExplorerSnapshot, error) {
	switch profile.ProviderID {
	case ProviderSQLite:
		return loadSQLiteExplorerSnapshot(ctx, profile, registry, secrets)
	default:
		return ExplorerSnapshot{}, fmt.Errorf("explorer metadata not implemented yet for provider %s", profile.ProviderID)
	}
}
