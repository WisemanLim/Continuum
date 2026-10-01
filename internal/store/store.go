// Package store opens the catalog database (SQLite for M1, PostgreSQL for M2)
// and applies the embedded, portable SQL migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps *sql.DB and rewrites `?` placeholders for PostgreSQL.
type DB struct {
	*sql.DB
	Dialect string // "sqlite" | "postgres"
}

// Open parses DATABASE_URL: sqlite:///./local.db, sqlite://./local.db, sqlite::memory:,
// postgres://… or postgresql://…
func Open(url string) (*DB, error) {
	switch {
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		db, err := sql.Open("pgx", url)
		if err != nil {
			return nil, err
		}
		return &DB{DB: db, Dialect: "postgres"}, nil
	case strings.HasPrefix(url, "sqlite:"):
		path := strings.TrimPrefix(url, "sqlite:")
		path = strings.TrimPrefix(path, "//")
		if strings.HasPrefix(path, "/./") { // sqlite:///./local.db → ./local.db
			path = path[1:]
		}
		dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
		if path == ":memory:" {
			dsn = "file::memory:?cache=shared&_pragma=foreign_keys(1)"
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // single writer; SQLite serialises writes anyway
		return &DB{DB: db, Dialect: "sqlite"}, nil
	default:
		return nil, fmt.Errorf("unsupported DATABASE_URL %q (want sqlite:… or postgres://…)", url)
	}
}

// Q rewrites `?` placeholders to `$n` for PostgreSQL.
func (d *DB) Q(q string) string {
	if d.Dialect != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (d *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, d.Q(q), args...)
}

func (d *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, d.Q(q), args...)
}

func (d *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, d.Q(q), args...)
}

// Tx runs fn in a transaction.
func (d *DB) Tx(ctx context.Context, fn func(tx *Tx) error) error {
	t, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{t: t, db: d}); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// Tx is a transaction with placeholder rewriting.
type Tx struct {
	t  *sql.Tx
	db *DB
}

func (t *Tx) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return t.t.ExecContext(ctx, t.db.Q(q), args...)
}

func (t *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.t.QueryContext(ctx, t.db.Q(q), args...)
}

func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return t.t.QueryRowContext(ctx, t.db.Q(q), args...)
}

// Migrate applies pending migrations in lexical order and returns the applied versions.
func (d *DB) Migrate(ctx context.Context) ([]string, error) {
	if _, err := d.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var n int
		if err := d.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		err = d.Tx(ctx, func(tx *Tx) error {
			for _, stmt := range splitSQL(string(body)) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("%s: %w\n%s", version, err, stmt)
				}
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, Now())
			return err
		})
		if err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}

// splitSQL splits on `;` at line ends and drops `--` comment lines.
func splitSQL(s string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		cur.WriteString(line + "\n")
		if strings.HasSuffix(t, ";") {
			out = append(out, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

// Now returns the canonical timestamp format (RFC3339Nano UTC).
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
