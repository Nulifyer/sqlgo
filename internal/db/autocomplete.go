package db

import (
	"context"
	"fmt"
)

type ObjectMetadata struct {
	Type      ExplorerObjectType
	Name      string
	Qualified string
	Columns   []string
}

type CompletionMetadata struct {
	Catalogs []string
	Schemas  []string
	Objects  []ObjectMetadata
}

func LoadCompletionMetadata(ctx context.Context, profile ConnectionProfile, registry *Registry) (CompletionMetadata, error) {
	return LoadCompletionMetadataWithSecrets(ctx, profile, registry, nil)
}

func LoadCompletionMetadataWithSecrets(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore) (CompletionMetadata, error) {
	switch profile.ProviderID {
	case ProviderSQLite:
		return loadSQLiteCompletionMetadata(ctx, profile, registry, secrets)
	default:
		return CompletionMetadata{}, fmt.Errorf("autocomplete metadata not implemented yet for provider %s", profile.ProviderID)
	}
}
