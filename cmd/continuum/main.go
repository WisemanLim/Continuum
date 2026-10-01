// continuum (alias ctm) — Continuum CLI (PRD §9).
package main

import (
	"os"

	"continuum/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}
