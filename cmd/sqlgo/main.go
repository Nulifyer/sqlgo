package main

import (
	"fmt"
	"os"

	"github.com/Nulifyer/sqlgo/internal/tui"
)

func main() {
	if err := tui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sqlgo:", err)
		os.Exit(1)
	}
}
