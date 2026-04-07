# Dev Data

This folder is for locally generated development databases and other scratch data used while building `sqlgo`.

The main helper command is:

```text
go run ./cmd/devdb
```

That command generates:

```text
dev-data/sqlgo-dev.db
```

The current fixture is intentionally much richer than a toy sample. It includes:

- 6 base tables
- 2 views
- hundreds of generated events
- hundreds of generated tasks
- 1,500 audit log rows
- CSV edge-case rows with commas, quotes, multiline text, unicode, and NULLs

To inspect the generated dataset:

```text
go run ./cmd/devdbdump
```

The generated SQLite database is intentionally ignored by git so we can recreate it freely during development.
