// ABOUTME: Database schema migrations and version management.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// migration represents a single schema migration with version, name, and SQL statements.
type migration struct {
	version    int
	name       string
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		name:    "init_core_tables",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS sandboxes (
				vmid INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				profile TEXT NOT NULL,
				state TEXT NOT NULL,
				ip TEXT,
				workspace_id TEXT,
				keepalive INTEGER NOT NULL DEFAULT 0,
				lease_expires_at TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				meta_json TEXT
			)`,
			`CREATE TABLE IF NOT EXISTS jobs (
				id TEXT PRIMARY KEY,
				repo_url TEXT NOT NULL,
				ref TEXT NOT NULL,
				profile TEXT NOT NULL,
				status TEXT NOT NULL,
				sandbox_vmid INTEGER,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				result_json TEXT
			)`,
			`CREATE TABLE IF NOT EXISTS profiles (
				name TEXT PRIMARY KEY,
				template_vmid INTEGER NOT NULL,
				yaml TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS workspaces (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				storage TEXT NOT NULL,
				volid TEXT NOT NULL,
				size_gb INTEGER NOT NULL,
				attached_vmid INTEGER,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				meta_json TEXT
			)`,
			`CREATE TABLE IF NOT EXISTS bootstrap_tokens (
				token TEXT PRIMARY KEY,
				vmid INTEGER NOT NULL,
				expires_at TEXT NOT NULL,
				consumed_at TEXT,
				created_at TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				ts TEXT NOT NULL,
				kind TEXT NOT NULL,
				sandbox_vmid INTEGER,
				job_id TEXT,
				msg TEXT,
				json TEXT
			)`,
			`CREATE INDEX IF NOT EXISTS idx_sandboxes_state ON sandboxes(state)`,
			`CREATE INDEX IF NOT EXISTS idx_sandboxes_profile ON sandboxes(profile)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_sandbox ON jobs(sandbox_vmid)`,
			`CREATE INDEX IF NOT EXISTS idx_workspaces_attached ON workspaces(attached_vmid)`,
			`CREATE INDEX IF NOT EXISTS idx_bootstrap_tokens_vmid ON bootstrap_tokens(vmid)`,
			`CREATE INDEX IF NOT EXISTS idx_events_sandbox ON events(sandbox_vmid)`,
			`CREATE INDEX IF NOT EXISTS idx_events_job ON events(job_id)`,
		},
	},
	{
		version: 2,
		name:    "add_job_spec_fields",
		statements: []string{
			`ALTER TABLE jobs ADD COLUMN task TEXT`,
			`ALTER TABLE jobs ADD COLUMN mode TEXT`,
			`ALTER TABLE jobs ADD COLUMN ttl_minutes INTEGER`,
			`ALTER TABLE jobs ADD COLUMN keepalive INTEGER NOT NULL DEFAULT 0`,
		},
	},
	{
		version: 3,
		name:    "add_artifacts",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS artifact_tokens (
				token TEXT PRIMARY KEY,
				job_id TEXT NOT NULL,
				vmid INTEGER,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				last_used_at TEXT,
				FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_artifact_tokens_job ON artifact_tokens(job_id)`,
			`CREATE INDEX IF NOT EXISTS idx_artifact_tokens_vmid ON artifact_tokens(vmid)`,
			`CREATE TABLE IF NOT EXISTS artifacts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				job_id TEXT NOT NULL,
				vmid INTEGER,
				name TEXT NOT NULL,
				path TEXT NOT NULL,
				size_bytes INTEGER NOT NULL,
				sha256 TEXT NOT NULL,
				mime TEXT,
				created_at TEXT NOT NULL,
				FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_artifacts_job ON artifacts(job_id)`,
			`CREATE INDEX IF NOT EXISTS idx_artifacts_vmid ON artifacts(vmid)`,
		},
	},
	{
		version: 4,
		name:    "add_sandbox_last_used_at",
		statements: []string{
			`ALTER TABLE sandboxes ADD COLUMN last_used_at TEXT`,
		},
	},
	{
		version: 5,
		name:    "add_exposures",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS exposures (
				name TEXT PRIMARY KEY,
				vmid INTEGER NOT NULL,
				port INTEGER NOT NULL,
				target_ip TEXT NOT NULL,
				url TEXT,
				state TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				FOREIGN KEY(vmid) REFERENCES sandboxes(vmid) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_exposures_vmid ON exposures(vmid)`,
		},
	},
}

// Migrate runs any pending migrations against the provided database.
//
// This function:
//   - Enables foreign key constraints
//   - Validates migration definitions (no duplicates, ordered versions)
//   - Ensures schema_migrations table exists
//   - Loads previously applied migration versions
//   - Verifies applied migrations are still known
//   - Applies any pending migrations in transaction
//
// Migrations are applied in version order. Each migration runs in a
// separate transaction for atomicity. Returns an error if any step fails.
func Migrate(db *sql.DB) error {
	if db == nil {
		return errors.New("db is nil")
	}
	// Enable foreign key constraints in SQLite
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := validateMigrations(); err != nil {
		return err
	}
	if err := ensureSchemaMigrations(db); err != nil {
		return err
	}
	applied, err := loadAppliedVersions(db)
	if err != nil {
		return err
	}
	if err := verifyKnownMigrations(applied); err != nil {
		return err
	}
	for _, m := range migrations {
		if _, ok := applied[m.version]; ok {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}
	return nil
}

// ensureSchemaMigrations creates the schema_migrations tracking table if it doesn't exist.
//
// The schema_migrations table stores which migrations have been applied,
// ensuring each migration is only run once even if Migrate() is called
// multiple times.
func ensureSchemaMigrations(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// loadAppliedVersions returns a set of migration versions that have been applied.
//
// Queries the schema_migrations table to determine which migrations have
// already been run, returning them as a set for fast lookup.
func loadAppliedVersions(db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("list schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int]struct{})
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// verifyKnownMigrations ensures all applied migrations still exist in the codebase.
//
// This prevents a scenario where a migration was applied but then removed
// from the code, which would cause database schema drift. Returns an error
// if an applied migration version is not found in the defined migrations.
func verifyKnownMigrations(applied map[int]struct{}) error {
	known := make(map[int]struct{}, len(migrations))
	for _, m := range migrations {
		known[m.version] = struct{}{}
	}
	for version := range applied {
		if _, ok := known[version]; !ok {
			return fmt.Errorf("unknown schema migration version %d", version)
		}
	}
	return nil
}

// applyMigration executes a single migration within a transaction.
//
// Runs all SQL statements for the migration in order. If any statement
// fails, the transaction is rolled back. On success, records the migration
// in schema_migrations before committing. Returns an error on failure.
func applyMigration(db *sql.DB, m migration) error {
	if len(m.statements) == 0 {
		return fmt.Errorf("migration %d has no statements", m.version)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}
	for _, stmt := range m.statements {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		if _, err := tx.Exec(trimmed); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec migration %d: %w", m.version, err)
		}
	}
	appliedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`, m.version, m.name, appliedAt); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}
	return nil
}

// validateMigrations checks that all migrations are properly defined.
//
// Validates:
//   - At least one migration exists
//   - All version numbers are positive
//   - No duplicate version numbers
//   - Versions are in ascending order
//   - All migrations have names
//
// Returns an error if any validation fails.
func validateMigrations() error {
	if len(migrations) == 0 {
		return errors.New("no migrations defined")
	}
	seen := make(map[int]struct{}, len(migrations))
	prev := 0
	for _, m := range migrations {
		if m.version <= 0 {
			return fmt.Errorf("migration version must be positive: %d", m.version)
		}
		if _, ok := seen[m.version]; ok {
			return fmt.Errorf("duplicate migration version %d", m.version)
		}
		if m.version < prev {
			return fmt.Errorf("migration version %d is out of order", m.version)
		}
		if strings.TrimSpace(m.name) == "" {
			return fmt.Errorf("migration %d missing name", m.version)
		}
		seen[m.version] = struct{}{}
		prev = m.version
	}
	return nil
}
