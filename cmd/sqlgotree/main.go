// sqlgotree is a dev utility that opens the three compose.yaml test
// databases and prints their schema trees. Credentials default to the
// dev fixtures in compose.yaml but can be overridden with flags so the
// binary does not carry baked-in secrets if reused elsewhere.
package main

import (
	"context"
	"flag"
	"fmt"
	"sort"

	"github.com/Nulifyer/sqlgo/internal/db"
	_ "github.com/Nulifyer/sqlgo/internal/db/mssql"
	_ "github.com/Nulifyer/sqlgo/internal/db/mysql"
	_ "github.com/Nulifyer/sqlgo/internal/db/postgres"
	_ "github.com/Nulifyer/sqlgo/internal/db/sqlite"
)

type target struct {
	label  string
	driver string
	cfg    db.Config
}

func main() {
	mssqlPass := flag.String("mssql-pass", "SqlGo_dev_Pass1!", "mssql password (compose.yaml fixture default)")
	pgPass := flag.String("pg-pass", "sqlgo_dev", "postgres password (compose.yaml fixture default)")
	mysqlPass := flag.String("mysql-pass", "sqlgo_dev", "mysql password (compose.yaml fixture default)")
	flag.Parse()

	ctx := context.Background()
	targets := []target{
		{"mssql", "mssql", db.Config{Host: "localhost", Port: 11433, User: "sa", Password: *mssqlPass, Database: "acmewidgets", Options: map[string]string{"encrypt": "disable"}}},
		{"postgres", "postgres", db.Config{Host: "localhost", Port: 15432, User: "sqlgo", Password: *pgPass, Database: "acmewidgets", Options: map[string]string{"sslmode": "disable"}}},
		{"mysql", "mysql", db.Config{Host: "localhost", Port: 13306, User: "sqlgo", Password: *mysqlPass, Database: "acmewidgets"}},
	}
	for _, t := range targets {
		fmt.Printf("\n=== %s ===\n", t.label)
		drv, err := db.Get(t.driver)
		if err != nil {
			fmt.Println("drv:", err)
			continue
		}
		conn, err := drv.Open(ctx, t.cfg)
		if err != nil {
			fmt.Println("open:", err)
			continue
		}
		info, err := conn.Schema(ctx)
		if err != nil {
			fmt.Println("schema:", err)
			conn.Close()
			continue
		}
		render(info)
		conn.Close()
	}
}

type bucket struct {
	tables, views, procs, funcs, triggers []string
}

func render(info *db.SchemaInfo) {
	user := map[string]*bucket{}
	sys := map[string]*bucket{}
	get := func(m map[string]*bucket, s string) *bucket {
		if b, ok := m[s]; ok {
			return b
		}
		b := &bucket{}
		m[s] = b
		return b
	}
	for _, t := range info.Tables {
		m := user
		if t.System {
			m = sys
		}
		b := get(m, t.Schema)
		if t.Kind == db.TableKindView {
			b.views = append(b.views, t.Name)
		} else {
			b.tables = append(b.tables, t.Name)
		}
	}
	for _, r := range info.Routines {
		m := user
		if r.System {
			m = sys
		}
		b := get(m, r.Schema)
		switch r.Kind {
		case db.RoutineKindProcedure:
			b.procs = append(b.procs, r.Name)
		default:
			b.funcs = append(b.funcs, r.Name)
		}
	}
	for _, tr := range info.Triggers {
		m := user
		if tr.System {
			m = sys
		}
		b := get(m, tr.Schema)
		b.triggers = append(b.triggers, fmt.Sprintf("%s (on %s, %s %s)", tr.Name, tr.Table, tr.Timing, tr.Event))
	}
	dump := func(label string, m map[string]*bucket) {
		if len(m) == 0 {
			return
		}
		fmt.Println(label)
		schemas := make([]string, 0, len(m))
		for s := range m {
			schemas = append(schemas, s)
		}
		sort.Strings(schemas)
		for _, s := range schemas {
			b := m[s]
			sort.Strings(b.tables)
			sort.Strings(b.views)
			sort.Strings(b.procs)
			sort.Strings(b.funcs)
			sort.Strings(b.triggers)
			name := s
			if name == "" {
				name = "(flat)"
			}
			fmt.Printf("  %s\n", name)
			sub := func(label string, items []string) {
				if len(items) == 0 {
					return
				}
				fmt.Printf("    %s (%d)\n", label, len(items))
				for _, it := range items {
					fmt.Printf("      - %s\n", it)
				}
			}
			sub("Tables", b.tables)
			sub("Views", b.views)
			sub("Procedures", b.procs)
			sub("Functions", b.funcs)
			sub("Triggers", b.triggers)
		}
	}
	dump("[user]", user)
	if len(sys) > 0 {
		fmt.Println("[sys]")
		schemas := make([]string, 0, len(sys))
		for s := range sys {
			schemas = append(schemas, s)
		}
		sort.Strings(schemas)
		for _, s := range schemas {
			b := sys[s]
			fmt.Printf("  %s  tables=%d views=%d procs=%d funcs=%d triggers=%d\n",
				s, len(b.tables), len(b.views), len(b.procs), len(b.funcs), len(b.triggers))
		}
	}
}
