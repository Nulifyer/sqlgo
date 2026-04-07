package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:dev-data/sqlgo-dev.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	counts := []struct {
		title string
		query string
	}{
		{"users", `SELECT COUNT(*) FROM users`},
		{"projects", `SELECT COUNT(*) FROM projects`},
		{"events", `SELECT COUNT(*) FROM events`},
		{"tasks", `SELECT COUNT(*) FROM tasks`},
		{"audit_logs", `SELECT COUNT(*) FROM audit_logs`},
		{"csv_edge_cases", `SELECT COUNT(*) FROM csv_edge_cases`},
		{"views", `SELECT COUNT(*) FROM sqlite_master WHERE type = 'view' AND name NOT LIKE 'sqlite_%'`},
	}

	fmt.Println("== row counts ==")
	for _, item := range counts {
		var count int
		if err := db.QueryRow(item.query).Scan(&count); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%-16s %d\n", item.title, count)
	}
	fmt.Println()

	dump("projects sample", `SELECT id, name, status, priority, COALESCE(CAST(budget AS TEXT), 'NULL') AS budget FROM projects ORDER BY id LIMIT 8`, db)
	dump("tasks sample", `SELECT id, project_id, assignee_user_id, status, estimate_hours, spent_hours, tags FROM tasks ORDER BY id LIMIT 12`, db)
	dump("csv edge cases", `SELECT id, label, COALESCE(raw_value, 'NULL') AS raw_value, expected_behavior FROM csv_edge_cases ORDER BY id`, db)
	dump("recent audit failures", `SELECT id, connection_name, database_name, statement_kind, duration_ms, error_text FROM recent_audit_failures LIMIT 10`, db)
}

func dump(title, query string, db *sql.DB) {
	rows, err := db.Query(query)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("== %s ==\n", title)
	fmt.Println(cols)

	values := make([]any, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(scanArgs...); err != nil {
			log.Fatal(err)
		}

		for i, value := range values {
			if i > 0 {
				fmt.Print(" | ")
			}
			switch typed := value.(type) {
			case nil:
				fmt.Print("NULL")
			case []byte:
				fmt.Print(string(typed))
			default:
				fmt.Print(typed)
			}
		}
		fmt.Println()
	}

	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Println()
}
