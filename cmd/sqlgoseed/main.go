// sqlgoseed populates a database with a fictional-company dataset for
// development and manual testing of the sqlgo TUI. It uses the same driver
// registry as the main binary, so every engine sqlgo supports can be seeded
// with identical logical content.
//
// Usage:
//
//	go run ./cmd/sqlgoseed -driver mssql \
//	    -host localhost -port 11433 \
//	    -user sa -pass 'SqlGo_dev_Pass1!' \
//	    -db acmewidgets \
//	    -scale 5
//
// The -scale flag multiplies base row counts. scale=1 is ~3k-5k rows total;
// scale=10 is ~30k-50k; scale=100 is hundreds of thousands.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	"github.com/Nulifyer/sqlgo/internal/seed"
)

func main() {
	var (
		driverName = flag.String("driver", "mssql", "registered driver name")
		host       = flag.String("host", "localhost", "database host")
		port       = flag.Int("port", 11433, "database port")
		user       = flag.String("user", "sa", "username")
		pass       = flag.String("pass", "SqlGo_dev_Pass1!", "password")
		database   = flag.String("db", "", "target database (must exist)")
		opts       = flag.String("opts", "encrypt=disable", "comma-separated key=value driver options")
		scale      = flag.Int("scale", 1, "row-count multiplier (1 = ~3k rows; 10 = ~30k)")
		seedVal    = flag.Uint64("seed", 42, "RNG seed for deterministic data")
		noDrop     = flag.Bool("no-drop", false, "keep existing tables instead of dropping them first")
		timeout    = flag.Duration("timeout", 10*time.Minute, "total timeout for the seeding run")
	)
	flag.Parse()

	cfg := db.Config{
		Host:     *host,
		Port:     *port,
		User:     *user,
		Password: *pass,
		Database: *database,
		Options:  parseOpts(*opts),
	}

	d, err := db.Get(*driverName)
	if err != nil {
		die(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, err := d.Open(ctx, cfg)
	if err != nil {
		die(fmt.Errorf("open: %w", err))
	}
	defer conn.Close()

	start := time.Now()
	err = seed.Run(ctx, conn, seed.Options{
		Scale:    *scale,
		Seed:     *seedVal,
		Drop:     !*noDrop,
		Progress: func(msg string) { fmt.Printf("[%s] %s\n", time.Since(start).Round(time.Millisecond), msg) },
	})
	if err != nil {
		die(err)
	}
	fmt.Printf("seed complete in %s\n", time.Since(start).Round(time.Millisecond))
}

func parseOpts(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "sqlgoseed:", err)
	os.Exit(1)
}
