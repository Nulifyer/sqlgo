// Package aliases registers label-only drivers that share a wire
// protocol with an existing adapter. Import for side effects. The
// aliases appear in db.Registered() and the connect form, but Open
// delegates to the base driver: MariaDB over mysql, CockroachDB /
// Supabase / Neon / Yugabyte / Timescale / Redshift over postgres.
package aliases

import (
	"github.com/Nulifyer/sqlgo/internal/db"

	// Base drivers must register before we alias them.
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
)

func init() {
	db.RegisterAlias("mariadb", "mysql")
	db.RegisterAlias("sqlserver", "mssql")
	db.RegisterAlias("cockroachdb", "postgres")
	db.RegisterAlias("supabase", "postgres")
	db.RegisterAlias("neon", "postgres")
	db.RegisterAlias("yugabytedb", "postgres")
	db.RegisterAlias("timescaledb", "postgres")
	// Amazon Redshift speaks Postgres wire protocol. Default port is
	// 5439 (not 5432) and SSL is typically required -- the user edits
	// Port + sslmode in the form; defaults stay postgres-native.
	db.RegisterAlias("redshift", "postgres")
}
