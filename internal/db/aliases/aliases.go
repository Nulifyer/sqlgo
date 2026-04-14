// Package aliases registers label-only drivers that share a wire
// protocol with an existing adapter. Import for side effects. The
// aliases appear in db.Registered() and the connect form, but Open
// delegates to the base driver: MariaDB over mysql, CockroachDB /
// Supabase / Neon / Yugabyte / Timescale over postgres.
package aliases

import (
	"github.com/Nulifyer/sqlgo/internal/db"

	// Base drivers must register before we alias them.
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
)

func init() {
	db.RegisterAlias("mariadb", "mysql")
	db.RegisterAlias("cockroachdb", "postgres")
	db.RegisterAlias("supabase", "postgres")
	db.RegisterAlias("neon", "postgres")
	db.RegisterAlias("yugabytedb", "postgres")
	db.RegisterAlias("timescaledb", "postgres")
}
