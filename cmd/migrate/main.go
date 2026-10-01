// migrate — applies embedded catalog migrations to DATABASE_URL (`make db-migrate`).
package main

import (
	"context"
	"fmt"
	"os"

	"continuum/internal/store"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "up" {
		fmt.Fprintln(os.Stderr, "usage: migrate up")
		os.Exit(2)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "sqlite:///./local.db"
	}
	db, err := store.Open(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	defer db.Close()
	applied, err := db.Migrate(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	if len(applied) == 0 {
		fmt.Printf("migrate up: already current (%s)\n", db.Dialect)
		return
	}
	fmt.Printf("migrate up: applied %v (%s)\n", applied, db.Dialect)
}
