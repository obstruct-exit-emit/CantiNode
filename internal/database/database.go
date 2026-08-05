// Package database owns CantiNode's SQLite connection and embedded schema
// migrations. See rootfolders.go, artists.go, albums.go, tracks.go, and
// trackfiles.go for the CRUD surface over each table defined here.
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a *sql.DB opened against CantiNode's SQLite database, with
// migrations already applied.
type DB struct {
	*sql.DB
}

// Open opens (creating if necessary) the SQLite database at dsn and applies
// any migrations that haven't run yet. dsn is a modernc.org/sqlite data
// source, e.g. a file path or ":memory:".
func Open(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps migration ordering predictable and avoids
	// SQLite "database is locked" errors under modernc.org/sqlite's driver
	// — the same tradeoff AcerviNode's database package makes. It also
	// means the foreign_keys pragma (per-connection, off by default in
	// SQLite) stays in effect for every query, enabling the track_files/
	// albums/tracks cascade deletes.
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	// WAL + synchronous=NORMAL instead of SQLite's own defaults (a rollback
	// journal, synchronous=FULL): each write is an append rather than a
	// full-file fsync, which matters here since SetMaxOpenConns(1) already
	// serializes every operation through one connection — a slow fsync on
	// one write directly delays everyone else's turn, including the web
	// UI's own polling. A no-op on an in-memory (":memory:") database, e.g.
	// in tests.
	if _, err := sqlDB.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL journal mode: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA synchronous = NORMAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set synchronous=NORMAL: %w", err)
	}

	db := &DB{DB: sqlDB}
	if err := db.migrate(context.Background()); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC()); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// migrationVersion extracts the leading integer from a migration filename
// like "0001_init.sql" -> 1.
func migrationVersion(filename string) (int, error) {
	prefix, _, ok := strings.Cut(filename, "_")
	if !ok {
		return 0, fmt.Errorf("migration filename %q missing '_' separator", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration filename %q has non-numeric version: %w", filename, err)
	}
	return version, nil
}
