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

	dump("users", `SELECT id, username, display_name, email, created_at FROM users ORDER BY id`, db)
	dump("projects", `SELECT id, owner_user_id, name, status, COALESCE(CAST(budget AS TEXT), 'NULL'), created_at FROM projects ORDER BY id`, db)
	dump("events", `SELECT id, project_id, event_type, message, created_at FROM events ORDER BY id`, db)
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
