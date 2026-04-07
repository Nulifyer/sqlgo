package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

func CreateSQLiteFixture(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer conn.Close()

	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			email TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			owner_user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			budget DECIMAL(12,2),
			notes TEXT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(owner_user_id) REFERENCES users(id)
		);`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id)
		);`,
		`INSERT INTO users (id, username, display_name, email, created_at) VALUES
			(1, 'alice', 'Alice Nguyen', 'alice@example.com', '2026-01-10T09:00:00Z'),
			(2, 'bob', 'Bob Chen', 'bob@example.com', '2026-01-12T14:30:00Z'),
			(3, 'casey', 'Casey Patel', 'casey@example.com', '2026-01-20T08:15:00Z');`,
		`INSERT INTO projects (id, owner_user_id, name, status, budget, notes, created_at) VALUES
			(100, 1, 'sqlgo', 'active', 125000.50, 'Terminal-first SQL app', '2026-02-01T10:00:00Z'),
			(101, 2, 'warehouse sync', 'paused', 8900.00, 'Needs CSV export fixes, quoting and multiline values', '2026-02-03T16:45:00Z'),
			(102, 3, 'client audit', 'draft', NULL, 'Contains commas, quotes ""like this"", and line breaks
for export testing.', '2026-02-08T11:20:00Z');`,
		`INSERT INTO events (id, project_id, event_type, message, created_at) VALUES
			(1000, 100, 'deploy', 'Initial prototype shipped', '2026-03-01T12:00:00Z'),
			(1001, 100, 'query', 'SELECT * FROM projects WHERE status = ''active''', '2026-03-02T13:05:00Z'),
			(1002, 101, 'warning', 'CSV output had embedded commas and needed quoting', '2026-03-03T08:25:00Z'),
			(1003, 102, 'note', 'Manual review pending', '2026-03-04T18:10:00Z');`,
	}

	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec fixture statement: %w", err)
		}
	}

	return nil
}
