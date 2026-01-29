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
	dataDirPerms = 0o750
)

// Store holds the SQLite handle for agentlabd.
type Store struct {
	Path string
	DB   *sql.DB
}

// Open connects to SQLite, applies pragmas, and runs migrations.
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
