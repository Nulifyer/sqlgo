package db

import (
	"context"
	"fmt"
)

func loadSQLiteExplorerSnapshot(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore) (ExplorerSnapshot, error) {
	result := ExplorerSnapshot{
		Databases: []ExplorerObject{
			{
				Type:        ExplorerDatabase,
				Name:        profile.Name,
				Qualified:   profile.Name,
				Description: "SQLite file",
			},
		},
	}

	conn, _, err := OpenWithSecrets(profile, registry, secrets)
	if err != nil {
		return ExplorerSnapshot{}, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT type, name
		FROM sqlite_master
		WHERE type IN ('table', 'view')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name`,
	)
	if err != nil {
		return ExplorerSnapshot{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var objectType string
		var name string
		if err := rows.Scan(&objectType, &name); err != nil {
			return ExplorerSnapshot{}, err
		}

		object := ExplorerObject{
			Name:      name,
			Qualified: quoteSQLiteIdentifier(name),
		}

		switch objectType {
		case "table":
			object.Type = ExplorerTable
			object.Description = fmt.Sprintf("SELECT * FROM %s LIMIT 25", object.Qualified)
			result.Tables = append(result.Tables, object)
		case "view":
			object.Type = ExplorerView
			object.Description = fmt.Sprintf("SELECT * FROM %s LIMIT 25", object.Qualified)
			result.Views = append(result.Views, object)
		}
	}

	if err := rows.Err(); err != nil {
		return ExplorerSnapshot{}, err
	}

	return result, nil
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + replaceAll(name, `"`, `""`) + `"`
}
