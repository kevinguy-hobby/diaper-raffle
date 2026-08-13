// Package store is the persistence layer: SQLite, one file on disk, no server
// to run. Every exported method takes a context and returns domain structs;
// nothing above this package writes SQL.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Migrations are applied in filename order, so they are numbered. A file's
// contents must never change once it has shipped — an existing database has
// already run it and will only see what comes after.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Sentinel errors the HTTP layer maps onto status codes.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
)

// Store owns the database handle.
type Store struct {
	db *sql.DB
}

// memoryDBs numbers throwaway databases so two of them never collide.
var memoryDBs atomic.Int64

// Open connects to the SQLite file at path and brings the schema up to date.
// The path ":memory:" gives a fresh throwaway database, which is what the
// tests use.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	if path == ":memory:" {
		// A shared cache keeps the database alive for as long as a connection
		// is open, and the unique name keeps concurrent callers — parallel
		// tests, mostly — from landing in each other's data.
		dsn = fmt.Sprintf("file:memdb%d?mode=memory&cache=shared&_pragma=foreign_keys(1)",
			memoryDBs.Add(1))
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite takes one writer at a time. Serialising here turns what would be
	// SQLITE_BUSY errors under concurrent draws into a short wait instead.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for health checks.
func (s *Store) DB() *sql.DB { return s.db }

// migrate brings the schema up to date, tracked by SQLite's user_version.
// Each file runs once, in filename order, inside its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	migrations := make([]string, len(entries))
	for i, name := range entries {
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		migrations[i] = string(body)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= len(migrations) {
		return nil
	}

	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", i+1, err)
		}
		// PRAGMA does not accept a bound parameter.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// tx runs fn inside a transaction, rolling back on error or panic.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// now is the single source of timestamps, stored as RFC 3339 in UTC so they
// sort lexically and read the same everywhere.
func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// isUniqueViolation reports whether err is SQLite refusing a duplicate.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
