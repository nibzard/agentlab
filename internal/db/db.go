// Package db provides SQLite database persistence for AgentLab.
//
// This package handles all database operations including:
//   - Database connection management with SQLite
//   - Schema migrations
//   - CRUD operations for sandboxes, jobs, workspaces, and artifacts
//   - Event logging and querying
//   - Token management for bootstrap and artifact upload
//
// The database uses SQLite with WAL mode for concurrent access and foreign
// key constraints for referential integrity. All operations are performed
// with prepared statements and transaction support.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	dataDirPerms = 0o750 // Permissions for database directory (owner full, group read+exec)
)

// Store holds the SQLite handle for agentlabd.
//
// The Store provides methods for all database operations. It uses a single
// connection with WAL mode to enable concurrent reads. Max open connections
// is limited to 1 to avoid write conflicts.
//
// Example usage:
//
//	store, err := db.Open("/var/lib/agentlab/agentlab.db")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer store.Close()
//
//	sandbox, err := store.GetSandbox(ctx, 1001)
type Store struct {
	Path string
	DB   *sql.DB
}

// Open connects to SQLite, applies pragmas, and runs migrations.
//
// This function:
//   - Creates the database directory if needed
//   - Opens a SQLite connection
//   - Configures connection limits (max 1 open connection)
//   - Applies SQLite pragmas (foreign_keys, WAL, busy_timeout)
//   - Verifies connectivity with Ping()
//   - Runs all pending migrations
//
// Parameters:
//   - path: Filesystem path to the SQLite database file
//
// Returns an error if the directory cannot be created, the database cannot
// be opened, or migrations fail.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := applyPragmas(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	if err := Migrate(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Store{Path: path, DB: conn}, nil
}

// Close releases the underlying database connection.
//
// It is safe to call Close on a nil Store or a Store with a nil DB.
// Returns any error from closing the database connection.
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func ensureDir(path string) error {
	if path == "" {
		return errors.New("db directory is required")
	}
	if err := os.MkdirAll(path, dataDirPerms); err != nil {
		return fmt.Errorf("create db dir %s: %w", path, err)
	}
	return nil
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}
	return nil
}
