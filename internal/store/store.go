// Package store wraps the SQLite database: connection management, schema
// migrations, and typed query helpers.
//
// We use modernc.org/sqlite (pure-Go, no cgo) so the binary cross-compiles
// without a C toolchain. Migrations live under migrations/ and are embedded
// into the binary at build time.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the database handle. It is safe for concurrent use.
type Store struct {
	DB *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs all pending
// migrations. The DSN enables WAL mode and reasonable busy/timeout settings
// for a single-process application. The parent directory is created if
// missing so a default like ".data/darknight.db" works on first run.
func Open(ctx context.Context, path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %s: %w", dir, err)
		}
	}
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// SQLite serialises writes; a small pool is enough, but allow a handful of
	// concurrent readers.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{DB: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Unicode-dependent backfill that SQL cannot express: migration 010 adds
	// movies.match_key, but the case/diacritic folding lives in Go.
	if err := s.backfillMatchKeys(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("backfill match_key: %w", err)
	}
	return s, nil
}

// backfillMatchKeys fills movies.match_key for rows that still have it empty
// (existing rows after migration 010, or rows written by an older binary).
// Idempotent and cheap: the WHERE clause matches nothing once caught up.
func (s *Store) backfillMatchKeys(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, title FROM movies WHERE match_key = '' AND title != ''`)
	if err != nil {
		return err
	}
	type pending struct {
		id  int64
		key string
	}
	var todo []pending
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			rows.Close()
			return err
		}
		if k := MatchKey(title); k != "" {
			todo = append(todo, pending{id, k})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range todo {
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE movies SET match_key = ? WHERE id = ?`, p.key, p.id); err != nil {
			return err
		}
	}
	return nil
}

// buildDSN turns a bare file path into a modernc/sqlite DSN with pragmas tuned
// for a local single-user app: WAL journaling, normal synchronous (durable
// enough, fast), foreign keys on, and a 5s busy timeout.
func buildDSN(path string) string {
	q := url.Values{}
	q.Set("_pragma", "journal_mode(WAL)")
	q.Set("_pragma", "synchronous(NORMAL)")
	q.Set("_pragma", "foreign_keys(ON)")
	q.Set("_pragma", "busy_timeout(5000)")
	q.Set("_time_format", "sqlite")
	return "file:" + path + "?" + q.Encode()
}

// Close releases the database handle.
func (s *Store) Close() error { return s.DB.Close() }

// migrate applies any embedded .sql files that have not run yet, in lexical
// order. A `schema_migrations` table tracks which files have been applied.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL DEFAULT (strftime('%s','now'))
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := s.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		// strip directory prefix -> version key
		version := strings.TrimPrefix(name, "migrations/")
		if applied[version] {
			continue
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := s.DB.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", version, err)
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}
	return nil
}

// Checkpoint forces a WAL checkpoint so all recent writes become visible in
// the main database file. Useful after a large scan finishes.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Snapshot writes a consistent copy of the whole database (WAL contents
// included) to dest using SQLite's VACUUM INTO. The source database is only
// read, so it is safe to snapshot while the app is live. dest must not already
// exist - SQLite refuses to overwrite. Used by `rescan --dry-run`, which scans
// a throwaway snapshot so the real database stays untouched.
func (s *Store) Snapshot(ctx context.Context, dest string) error {
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}

func (s *Store) appliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}
