package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/nulifyer/sqlgo/internal/db"
)

func main() {
	target := filepath.Join("dev-data", "sqlgo-dev.db")
	if err := db.CreateSQLiteFixture(context.Background(), target); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("generated %s\n", target)
}
