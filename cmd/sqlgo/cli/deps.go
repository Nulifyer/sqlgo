package cli

import (
	"context"
	"io"

	"github.com/Nulifyer/sqlgo/internal/config"
	"github.com/Nulifyer/sqlgo/internal/connectutil"
	"github.com/Nulifyer/sqlgo/internal/secret"
	"github.com/Nulifyer/sqlgo/internal/store"
)

type cliStore interface {
	Close() error
	GetConnection(ctx context.Context, name string) (config.Connection, error)
	ListConnections(ctx context.Context) ([]config.Connection, error)
	SaveConnection(ctx context.Context, oldName string, c config.Connection) error
	DeleteConnection(ctx context.Context, name string) error
	ExportJSON(ctx context.Context, w io.Writer) error
	ImportJSON(ctx context.Context, r io.Reader) (int, error)
	ClearHistory(ctx context.Context, connectionName string) (int64, error)
	ListRecentHistory(ctx context.Context, connectionName string, limit int) ([]store.HistoryEntry, error)
	SearchHistory(ctx context.Context, connectionName, q string, limit int) ([]store.HistoryEntry, error)
	RecordHistory(ctx context.Context, e store.HistoryEntry) error
}

var openStoreFn = func(ctx context.Context) (cliStore, error) {
	return store.Open(ctx)
}

var secretStoreFactory = func() secret.Store {
	return secret.System()
}

var terminalDetector = isTerminal

var runtimeDepsFactory = func() connectutil.RuntimeDeps {
	return connectutil.DefaultRuntimeDeps(secretStoreFactory())
}
