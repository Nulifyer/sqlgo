package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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

	if err := createSQLiteFixtureSchema(ctx, conn); err != nil {
		return err
	}

	if err := seedSQLiteFixtureData(ctx, conn); err != nil {
		return err
	}

	return nil
}

func createSQLiteFixtureSchema(ctx context.Context, conn *sql.DB) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			email TEXT NOT NULL,
			team TEXT NOT NULL,
			location TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			owner_user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			budget DECIMAL(12,2),
			notes TEXT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(owner_user_id) REFERENCES users(id)
		);`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
			payload_json TEXT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id)
		);`,
		`CREATE TABLE tasks (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			assignee_user_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			status TEXT NOT NULL,
			estimate_hours INTEGER NOT NULL,
			spent_hours INTEGER NOT NULL,
			tags TEXT NOT NULL,
			details TEXT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id),
			FOREIGN KEY(assignee_user_id) REFERENCES users(id)
		);`,
		`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY,
			connection_name TEXT NOT NULL,
			database_name TEXT NOT NULL,
			statement_kind TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			rows_affected INTEGER NOT NULL,
			success INTEGER NOT NULL,
			query_text TEXT NOT NULL,
			error_text TEXT NULL,
			ran_at TEXT NOT NULL
		);`,
		`CREATE TABLE csv_edge_cases (
			id INTEGER PRIMARY KEY,
			label TEXT NOT NULL,
			raw_value TEXT NULL,
			expected_behavior TEXT NOT NULL
		);`,
		`CREATE VIEW active_projects AS
			SELECT
				p.id,
				p.name,
				p.status,
				u.display_name AS owner_name,
				p.budget,
				p.created_at
			FROM projects p
			JOIN users u ON u.id = p.owner_user_id
			WHERE p.status = 'active';`,
		`CREATE VIEW recent_audit_failures AS
			SELECT
				id,
				connection_name,
				database_name,
				statement_kind,
				duration_ms,
				error_text,
				ran_at
			FROM audit_logs
			WHERE success = 0
			ORDER BY ran_at DESC;`,
	}

	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("exec fixture schema: %w", err)
		}
	}

	return nil
}

func seedSQLiteFixtureData(ctx context.Context, conn *sql.DB) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := seedUsers(ctx, tx); err != nil {
		return err
	}
	if err := seedProjects(ctx, tx); err != nil {
		return err
	}
	if err := seedEvents(ctx, tx); err != nil {
		return err
	}
	if err := seedTasks(ctx, tx); err != nil {
		return err
	}
	if err := seedAuditLogs(ctx, tx); err != nil {
		return err
	}
	if err := seedCSVEdgeCases(ctx, tx); err != nil {
		return err
	}

	return tx.Commit()
}

func seedUsers(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO users (id, username, display_name, email, team, location, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	type userRow struct {
		id          int
		username    string
		displayName string
		email       string
		team        string
		location    string
		createdAt   string
	}

	users := []userRow{
		{1, "alice", "Alice Nguyen", "alice@example.com", "platform", "New York", "2026-01-10T09:00:00Z"},
		{2, "bob", "Bob Chen", "bob@example.com", "data", "Chicago", "2026-01-12T14:30:00Z"},
		{3, "casey", "Casey Patel", "casey@example.com", "consulting", "Austin", "2026-01-20T08:15:00Z"},
		{4, "drew", "Drew Martinez", "drew@example.com", "platform", "Seattle", "2026-01-25T17:40:00Z"},
		{5, "erin", "Erin Thompson", "erin@example.com", "analytics", "Boston", "2026-01-27T11:05:00Z"},
		{6, "fran", "Fran Lee", "fran@example.com", "ops", "Denver", "2026-01-31T07:55:00Z"},
	}

	for _, user := range users {
		if _, err := stmt.ExecContext(ctx, user.id, user.username, user.displayName, user.email, user.team, user.location, user.createdAt); err != nil {
			return fmt.Errorf("insert user %d: %w", user.id, err)
		}
	}

	return nil
}

func seedProjects(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO projects (id, owner_user_id, name, status, priority, budget, notes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	type projectRow struct {
		id        int
		ownerID   int
		name      string
		status    string
		priority  string
		budget    any
		notes     any
		createdAt string
	}

	projects := []projectRow{
		{100, 1, "sqlgo", "active", "high", 125000.50, "Terminal-first SQL app for work and personal use.", "2026-02-01T10:00:00Z"},
		{101, 2, "warehouse sync", "paused", "medium", 8900.00, "Needs CSV export fixes, quoting, and multiline values.", "2026-02-03T16:45:00Z"},
		{102, 3, "client audit", "draft", "high", nil, "Contains commas, quotes \"like this\", and line breaks\nfor export testing.", "2026-02-08T11:20:00Z"},
		{103, 4, "incident dashboard", "active", "urgent", 44250.75, "Heavy query workload with wide result sets.", "2026-02-12T09:10:00Z"},
		{104, 5, "billing cleanup", "active", "medium", 11200.00, "Null-heavy data model; lots of ad hoc reporting.", "2026-02-14T08:00:00Z"},
		{105, 6, "support analytics", "completed", "low", 5000.00, nil, "2026-02-18T13:30:00Z"},
		{106, 1, "migration rehearsal", "active", "urgent", 68000.00, strings.Repeat("Long-note-block-", 18), "2026-02-22T07:25:00Z"},
		{107, 2, "sybase extraction", "active", "high", 25400.00, "Used to test older server compatibility and weird encodings.", "2026-02-26T15:05:00Z"},
	}

	for _, project := range projects {
		if _, err := stmt.ExecContext(ctx, project.id, project.ownerID, project.name, project.status, project.priority, project.budget, project.notes, project.createdAt); err != nil {
			return fmt.Errorf("insert project %d: %w", project.id, err)
		}
	}

	return nil
}

func seedEvents(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events (id, project_id, event_type, severity, message, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	base := []struct {
		id        int
		projectID int
		eventType string
		severity  string
		message   string
		payload   any
		createdAt string
	}{
		{1000, 100, "deploy", "info", "Initial prototype shipped", `{"commit":"abc123","env":"dev"}`, "2026-03-01T12:00:00Z"},
		{1001, 100, "query", "info", "SELECT * FROM projects WHERE status = 'active'", `{"rows":7}`, "2026-03-02T13:05:00Z"},
		{1002, 101, "warning", "warn", "CSV output had embedded commas and needed quoting", `{"field":"notes"}`, "2026-03-03T08:25:00Z"},
		{1003, 102, "note", "info", "Manual review pending", nil, "2026-03-04T18:10:00Z"},
		{1004, 103, "error", "error", "Timeout during large aggregation query", `{"timeout_ms":30000}`, "2026-03-05T09:15:00Z"},
		{1005, 104, "import", "info", "Loaded 482 billing corrections", `{"source":"billing.csv"}`, "2026-03-06T10:45:00Z"},
	}

	for _, event := range base {
		if _, err := stmt.ExecContext(ctx, event.id, event.projectID, event.eventType, event.severity, event.message, event.payload, event.createdAt); err != nil {
			return fmt.Errorf("insert base event %d: %w", event.id, err)
		}
	}

	id := 1100
	start := time.Date(2026, 3, 7, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 240; i++ {
		projectID := 100 + (i % 8)
		severity := []string{"info", "warn", "error"}[i%3]
		payload := fmt.Sprintf(`{"batch":%d,"rows":%d,"duration_ms":%d}`, i+1, (i%17)*113, 80+(i*7)%900)
		message := fmt.Sprintf("Generated audit event %03d for project %d with severity %s", i+1, projectID, severity)
		createdAt := start.Add(time.Duration(i) * 17 * time.Minute).Format(time.RFC3339)
		if _, err := stmt.ExecContext(ctx, id+i, projectID, "generated", severity, message, payload, createdAt); err != nil {
			return fmt.Errorf("insert generated event %d: %w", id+i, err)
		}
	}

	return nil
}

func seedTasks(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO tasks (id, project_id, assignee_user_id, title, status, estimate_hours, spent_hours, tags, details, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	statuses := []string{"todo", "in_progress", "blocked", "review", "done"}
	tagSets := []string{
		"sql,ui,terminal",
		"export,csv,quoting",
		"auth,azure,sso",
		"sqlite,fixture,testdata",
		"results,grid,scrolling",
		"sybase,compat,driver",
	}

	start := time.Date(2026, 3, 10, 8, 30, 0, 0, time.UTC)
	for i := 1; i <= 600; i++ {
		projectID := 100 + ((i - 1) % 8)
		assignee := 1 + ((i - 1) % 6)
		status := statuses[(i-1)%len(statuses)]
		estimate := 2 + (i % 13)
		spent := (i * 3) % (estimate + 6)
		title := fmt.Sprintf("Task %03d for project %d", i, projectID)
		tags := tagSets[(i-1)%len(tagSets)]
		var details any
		switch {
		case i%15 == 0:
			details = fmt.Sprintf("Wide detail block %03d: %s", i, strings.Repeat("long-text-", 24))
		case i%22 == 0:
			details = fmt.Sprintf("Multiline task detail %03d\nLine two with commas, quotes \"quoted value\", and tabs\tfor rendering.", i)
		case i%9 == 0:
			details = nil
		default:
			details = fmt.Sprintf("Normal task detail %03d", i)
		}

		updatedAt := start.Add(time.Duration(i*11) * time.Minute).Format(time.RFC3339)
		if _, err := stmt.ExecContext(ctx, 2000+i, projectID, assignee, title, status, estimate, spent, tags, details, updatedAt); err != nil {
			return fmt.Errorf("insert task %d: %w", i, err)
		}
	}

	return nil
}

func seedAuditLogs(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO audit_logs (id, connection_name, database_name, statement_kind, duration_ms, rows_affected, success, query_text, error_text, ran_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	connections := []string{"local-sqlite", "work-sqlserver", "azure-finance", "snowflake-bi", "lab-postgres"}
	databases := []string{"main", "master", "FinanceDW", "ANALYTICS", "postgres"}
	kinds := []string{"select", "insert", "update", "delete", "ddl"}
	start := time.Date(2026, 3, 15, 6, 0, 0, 0, time.UTC)

	for i := 1; i <= 1500; i++ {
		success := 1
		var errText any
		if i%17 == 0 || i%41 == 0 {
			success = 0
			errText = fmt.Sprintf("Simulated failure %d: deadlock or timeout in test log stream", i)
		}

		queryText := fmt.Sprintf("/* audit %04d */ SELECT * FROM tasks WHERE project_id = %d AND status = '%s';", i, 100+(i%8), []string{"todo", "in_progress", "blocked", "review", "done"}[i%5])
		ranAt := start.Add(time.Duration(i*3) * time.Minute).Format(time.RFC3339)
		if _, err := stmt.ExecContext(
			ctx,
			5000+i,
			connections[i%len(connections)],
			databases[i%len(databases)],
			kinds[i%len(kinds)],
			40+(i*13)%4200,
			(i*19)%12000,
			success,
			queryText,
			errText,
			ranAt,
		); err != nil {
			return fmt.Errorf("insert audit log %d: %w", i, err)
		}
	}

	return nil
}

func seedCSVEdgeCases(ctx context.Context, tx *sql.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO csv_edge_cases (id, label, raw_value, expected_behavior) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	rows := []struct {
		id       int
		label    string
		rawValue any
		expected string
	}{
		{1, "plain text", "hello world", "no quotes needed"},
		{2, "comma", "a,b,c", "quoted field"},
		{3, "double quote", `say "hello"`, "escaped quotes"},
		{4, "multiline", "line one\nline two\nline three", "quoted multiline field"},
		{5, "leading spaces", "   padded", "preserve whitespace"},
		{6, "trailing spaces", "padded   ", "preserve whitespace"},
		{7, "tab", "col1\tcol2", "tab retained"},
		{8, "null", nil, "empty or configured null marker"},
		{9, "unicode", "snowman ☃ and kanji 東京", "utf-8 output"},
		{10, "very wide", strings.Repeat("wide-value-", 40), "long text survives export"},
	}

	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, row.id, row.label, row.rawValue, row.expected); err != nil {
			return fmt.Errorf("insert csv edge case %d: %w", row.id, err)
		}
	}

	return nil
}
