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

The generated SQLite database is intentionally ignored by git so we can recreate it freely during development.

