# Connection DSN Notes

These are starter DSN examples for the current scaffold. They are meant to make early profile testing easier, not to be the final UX.

## SQL Server

SQL auth:

```text
sqlserver://user:password@localhost:1433?database=master
```

Windows auth on Windows:

```text
sqlserver://localhost:1433?database=master
```

## Azure SQL

Azure AD driver name is handled by the profile provider selection. The DSN still follows the SQL Server style:

```text
sqlserver://user@tenant.onmicrosoft.com:password@server.database.windows.net:1433?database=mydb&fedauth=ActiveDirectoryPassword
```

## PostgreSQL

```text
postgres://user:password@localhost:5432/postgres?sslmode=disable
```

## MySQL

```text
user:password@tcp(localhost:3306)/dbname?parseTime=true
```

## SQLite

```text
file:C:/data/app.db
```

## Snowflake

```text
user:password@account/db/schema?warehouse=wh&role=myrole
```

## Sybase ASE

```text
tds://user:password@localhost:5000/master?charset=utf8
```

## Important note

The current scaffold stores the DSN directly in the profile JSON file so we can move quickly. Secret storage is planned next so we can separate passwords and tokens from the main profile record.
