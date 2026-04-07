package db

import (
	"context"
	"fmt"
	"slices"
)

func loadSQLiteCompletionMetadata(ctx context.Context, profile ConnectionProfile, registry *Registry, secrets SecretStore) (CompletionMetadata, error) {
	conn, _, err := OpenWithSecrets(profile, registry, secrets)
	if err != nil {
		return CompletionMetadata{}, err
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
		return CompletionMetadata{}, err
	}
	defer rows.Close()

	meta := CompletionMetadata{
		Catalogs: []string{profile.Name},
	}

	for rows.Next() {
		var objectType string
		var name string
		if err := rows.Scan(&objectType, &name); err != nil {
			return CompletionMetadata{}, err
		}

		item := ObjectMetadata{
			Name:      name,
			Qualified: quoteSQLiteIdentifier(name),
		}
		switch objectType {
		case "table":
			item.Type = ExplorerTable
		case "view":
			item.Type = ExplorerView
		}

		columnRows, err := conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s);`, quoteSQLiteIdentifier(name)))
		if err != nil {
			return CompletionMetadata{}, err
		}
		for columnRows.Next() {
			var (
				cid      int
				column   string
				dataType string
				notNull  int
				defaultV any
				pk       int
			)
			if err := columnRows.Scan(&cid, &column, &dataType, &notNull, &defaultV, &pk); err != nil {
				columnRows.Close()
				return CompletionMetadata{}, err
			}
			item.Columns = append(item.Columns, column)
		}
		if err := columnRows.Close(); err != nil {
			return CompletionMetadata{}, err
		}

		slices.Sort(item.Columns)
		meta.Objects = append(meta.Objects, item)
	}

	if err := rows.Err(); err != nil {
		return CompletionMetadata{}, err
	}

	return meta, nil
}
