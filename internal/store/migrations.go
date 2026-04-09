package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations is the ordered list of schema changes applied to sqlgo.db.
// Append-only: each slice entry is a new version, and entries must never
// be reordered, edited, or deleted once shipped -- migrate() uses the
// index + 1 as the version number recorded in schema_migrations.
//
// Phase 1.6 adds `connections`; Phase 1.7 adds `history` + FTS5.
var migrations = []string{
	// v1: connections table.
	//
	// Name is the user-facing identifier and the primary key so looking
	// a connection up by display name is an index hit. Options is a JSON
	// object (TEXT) because sqlite lacks a map type; the store
	// marshals/unmarshals on the Go side. updated_at is refreshed on
	// every upsert so we can sort by most-recently-edited later.
	`CREATE TABLE connections (
        name       TEXT PRIMARY KEY,
        driver     TEXT NOT NULL,
        host       TEXT NOT NULL DEFAULT '',
        port       INTEGER NOT NULL DEFAULT 0,
        username   TEXT NOT NULL DEFAULT '',
        password   TEXT NOT NULL DEFAULT '',
        database   TEXT NOT NULL DEFAULT '',
        options    TEXT NOT NULL DEFAULT '{}',
        created_at TEXT NOT NULL DEFAULT (datetime('now')),
        updated_at TEXT NOT NULL DEFAULT (datetime('now'))
    )`,

	// v2: query history table.
	//
	// One row per executed query. connection_name is a soft reference
	// (no FK) so deleting a connection doesn't cascade-wipe the user's
	// past queries; they may still want to search through them. The
	// ring buffer (max ~1000 rows per connection) is enforced lazily on
	// insert by store.RecordHistory -- keeping the bound out of the
	// schema means an accidentally-large batch can still land and the
	// next insert trims it.
	//
	// row_count = -1 for queries that errored before a row count was
	// known; error IS NULL for successful runs.
	`CREATE TABLE history (
        id              INTEGER PRIMARY KEY AUTOINCREMENT,
        connection_name TEXT NOT NULL,
        sql             TEXT NOT NULL,
        executed_at     TEXT NOT NULL DEFAULT (datetime('now')),
        elapsed_ms      INTEGER NOT NULL DEFAULT 0,
        row_count       INTEGER NOT NULL DEFAULT 0,
        error           TEXT
    )`,
	`CREATE INDEX history_by_conn_time
        ON history(connection_name, executed_at DESC, id DESC)`,

	// v3: FTS5 index over the sql column.
	//
	// content='history' + content_rowid='id' makes it an "external
	// content" table: the FTS index stores only tokens, and triggers
	// keep it in sync with the base history table. This avoids
	// duplicating the raw sql text.
	`CREATE VIRTUAL TABLE history_fts USING fts5(
        sql,
        content='history',
        content_rowid='id',
        tokenize='unicode61 remove_diacritics 2'
    )`,
	`CREATE TRIGGER history_ai AFTER INSERT ON history BEGIN
        INSERT INTO history_fts(rowid, sql) VALUES (new.id, new.sql);
    END`,
	`CREATE TRIGGER history_ad AFTER DELETE ON history BEGIN
        INSERT INTO history_fts(history_fts, rowid, sql) VALUES ('delete', old.id, old.sql);
    END`,
	`CREATE TRIGGER history_au AFTER UPDATE ON history BEGIN
        INSERT INTO history_fts(history_fts, rowid, sql) VALUES ('delete', old.id, old.sql);
        INSERT INTO history_fts(rowid, sql) VALUES (new.id, new.sql);
    END`,

	// v4: SSH tunnel columns on connections.
	//
	// Columns are nullable / defaulted to empty strings so existing
	// rows keep working. ssh_host="" means no tunnel. ssh_password is
	// plaintext on disk when the OS keyring is unavailable; otherwise
	// it gets migrated into the keyring same as the db password.
	`ALTER TABLE connections ADD COLUMN ssh_host TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE connections ADD COLUMN ssh_port INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE connections ADD COLUMN ssh_user TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE connections ADD COLUMN ssh_password TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE connections ADD COLUMN ssh_key_path TEXT NOT NULL DEFAULT ''`,
}

// migrate brings the schema forward using the package-level migrations
// list. This is what OpenAt calls on every boot.
func migrate(ctx context.Context, db *sql.DB) error {
	return migrateWith(ctx, db, migrations)
}

// migrateWith applies an explicit migration list. Factored out so tests
// can inject a fixture list without racing with the package-level var.
// Idempotent: running against an up-to-date store is a no-op. Each
// migration runs inside its own transaction so a failure mid-way leaves
// the store at the previous clean version.
func migrateWith(ctx context.Context, db *sql.DB, list []string) error {
	// schema_migrations tracks which versions have been applied. Created
	// unconditionally so we have somewhere to read the current version
	// from before running anything.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at TEXT NOT NULL DEFAULT (datetime('now'))
    )`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read current version: %w", err)
	}

	for i := current; i < len(list); i++ {
		version := i + 1
		if err := applyMigration(ctx, db, version, list[i]); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, ddl string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration %d begin: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("migration %d ddl: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("migration %d record: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d commit: %w", version, err)
	}
	return nil
}
