package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrateSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := Open("sqlite:///" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 || applied[0] != "0001_init" {
		t.Fatalf("applied = %v", applied)
	}
	again, err := db.Migrate(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate should be a no-op: %v %v", again, err)
	}
	for _, table := range []string{"nodes", "volumes", "disks", "objects", "jobs", "job_items", "tombstones", "events", "audit"} {
		var n int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
}

func TestPlaceholderRewrite(t *testing.T) {
	d := &DB{Dialect: "postgres"}
	if got := d.Q("a = ? AND b = ?"); got != "a = $1 AND b = $2" {
		t.Fatal(got)
	}
}

func TestSQLitePathForms(t *testing.T) {
	for _, u := range []string{"sqlite:///./x.db", "sqlite://./x.db", "sqlite::memory:"} {
		if _, err := Open(u); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
}
