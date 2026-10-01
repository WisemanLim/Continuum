// Package storetest gives tests a fresh database: SQLite in a temp dir by default, or a new PostgreSQL
// database when CONTINUUM_TEST_PG is set (admin URL, e.g. postgresql://app:app@127.0.0.1:15432/postgres),
// so the same suites prove the shared SQL on both engines (FR-N08 central mode).
package storetest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// URL returns a DATABASE_URL for a new empty database (dropped when the test ends).
func URL(t testing.TB, dir string) string {
	t.Helper()
	admin := os.Getenv("CONTINUUM_TEST_PG")
	if admin == "" {
		return "sqlite:///" + filepath.Join(dir, "test.db")
	}
	b := make([]byte, 6)
	rand.Read(b)
	name := "ct_" + hex.EncodeToString(b)
	db, err := sql.Open("pgx", admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		db.Close()
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
