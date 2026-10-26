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
	// goMigrate, when set, runs inside the migration transaction after the SQL
	// statements. Use it for data backfills that cannot be expressed as static
	// SQL (review M1).
	goMigrate func(tx *sql.Tx) error
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
	{
		version: 6,
		name:    "add_jobs_workspace",
		statements: []string{
			`ALTER TABLE jobs ADD COLUMN workspace_id TEXT`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_workspace ON jobs(workspace_id)`,
		},
	},
	{
		version: 7,
		name:    "add_workspace_leases",
		statements: []string{
			`ALTER TABLE workspaces ADD COLUMN lease_owner TEXT`,
			`ALTER TABLE workspaces ADD COLUMN lease_nonce TEXT`,
			`ALTER TABLE workspaces ADD COLUMN lease_expires_at TEXT`,
			`CREATE INDEX IF NOT EXISTS idx_workspaces_lease_expires ON workspaces(lease_expires_at)`,
		},
	},
	{
		version: 8,
		name:    "add_messages",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				ts TEXT NOT NULL,
				scope_type TEXT NOT NULL,
				scope_id TEXT NOT NULL,
				author TEXT,
				kind TEXT,
				text TEXT,
				json TEXT
			)`,
			`CREATE INDEX IF NOT EXISTS idx_messages_scope ON messages(scope_type, scope_id, id)`,
			`CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages(ts)`,
		},
	},
	{
		version: 9,
		name:    "add_sessions",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS sessions (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				workspace_id TEXT NOT NULL,
				current_vmid INTEGER,
				profile TEXT NOT NULL,
				branch TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				meta_json TEXT
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_name ON sessions(name)`,
			`CREATE INDEX IF NOT EXISTS idx_sessions_workspace ON sessions(workspace_id)`,
			`CREATE INDEX IF NOT EXISTS idx_sessions_current_vmid ON sessions(current_vmid)`,
			`CREATE INDEX IF NOT EXISTS idx_sessions_branch ON sessions(branch)`,
		},
	},
	{
		version: 10,
		name:    "add_jobs_session",
		statements: []string{
			`ALTER TABLE jobs ADD COLUMN session_id TEXT`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_session ON jobs(session_id)`,
		},
	},
	{
		version: 11,
		name:    "add_workspace_snapshots",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS workspace_snapshots (
				workspace_id TEXT NOT NULL,
				name TEXT NOT NULL,
				backend_ref TEXT NOT NULL,
				created_at TEXT NOT NULL,
				meta_json TEXT,
				PRIMARY KEY (workspace_id, name),
				FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_workspace ON workspace_snapshots(workspace_id)`,
			`CREATE INDEX IF NOT EXISTS idx_workspace_snapshots_created ON workspace_snapshots(created_at)`,
		},
	},
	{
		version: 12,
		name:    "add_sandbox_type_and_image",
		statements: []string{
			`ALTER TABLE sandboxes ADD COLUMN type TEXT NOT NULL DEFAULT 'vm'`,
			`ALTER TABLE sandboxes ADD COLUMN image TEXT`,
		},
	},
	{
		version: 13,
		name:    "add_integrations",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS integrations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL UNIQUE,
				type TEXT NOT NULL,
				target TEXT NOT NULL DEFAULT '',
				encrypted_secret TEXT NOT NULL,
				secret_type TEXT NOT NULL DEFAULT 'bearer',
				secret_header TEXT NOT NULL DEFAULT '',
				username TEXT NOT NULL DEFAULT '',
				attach_mode TEXT NOT NULL,
				attach_selector TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_integrations_name ON integrations(name)`,
			`ALTER TABLE sandboxes ADD COLUMN tags TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 14,
		name:    "add_integration_provider",
		statements: []string{
			`ALTER TABLE integrations ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 15,
		name:    "add_sandbox_prompt",
		statements: []string{
			`ALTER TABLE sandboxes ADD COLUMN prompt TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 16,
		name:    "add_users_teams_audit",
		statements: []string{
			`CREATE TABLE IF NOT EXISTS users (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				role TEXT NOT NULL DEFAULT 'user',
				primary_fingerprint TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_name ON users(name)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_fingerprint ON users(primary_fingerprint)`,
			`CREATE TABLE IF NOT EXISTS user_ssh_keys (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL,
				fingerprint TEXT NOT NULL,
				public_key TEXT NOT NULL,
				comment TEXT NOT NULL DEFAULT '',
				added_at TEXT NOT NULL,
				FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_ssh_keys_fingerprint ON user_ssh_keys(fingerprint)`,
			`CREATE INDEX IF NOT EXISTS idx_user_ssh_keys_user ON user_ssh_keys(user_id)`,
			`CREATE TABLE IF NOT EXISTS teams (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL UNIQUE,
				description TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_teams_name ON teams(name)`,
			`CREATE TABLE IF NOT EXISTS team_members (
				team_id TEXT NOT NULL,
				user_id TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'user',
				joined_at TEXT NOT NULL,
				PRIMARY KEY (team_id, user_id),
				FOREIGN KEY(team_id) REFERENCES teams(id) ON DELETE CASCADE,
				FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_team_members_user ON team_members(user_id)`,
			`CREATE TABLE IF NOT EXISTS audit_log (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL DEFAULT '',
				action TEXT NOT NULL,
				resource TEXT NOT NULL DEFAULT '',
				detail TEXT NOT NULL DEFAULT '',
				timestamp TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id)`,
			`CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action)`,
			`CREATE INDEX IF NOT EXISTS idx_audit_log_timestamp ON audit_log(timestamp)`,
			`CREATE TABLE IF NOT EXISTS resource_quotas (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				scope_type TEXT NOT NULL,
				scope_id TEXT NOT NULL,
				max_sandboxes INTEGER NOT NULL DEFAULT 0,
				max_cpu INTEGER NOT NULL DEFAULT 0,
				max_ram_mb INTEGER NOT NULL DEFAULT 0,
				UNIQUE(scope_type, scope_id)
			)`,
			`ALTER TABLE sandboxes ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX IF NOT EXISTS idx_sandboxes_owner ON sandboxes(owner)`,
		},
	},
	{
		version: 17,
		name:    "normalize_timestamps_fixed_width",
		// No schema change; this migration rewrites every stored timestamp to
		// the fixed-width 9-digit-fractional form so SQLite TEXT ordering agrees
		// with chronological order within the same second (review M1). See
		// backfillTimestampColumns for the per-column logic.
		goMigrate: backfillTimestampColumns,
	},
	{
		version: 18,
		name:    "add_workspace_last_attached",
		// Detach clears attached_vmid, so the vmid of the last attachment is
		// kept separately. Scoped tokens use it to refuse attach on detached
		// workspaces that belong to another scope (review F5).
		statements: []string{
			`ALTER TABLE workspaces ADD COLUMN last_attached_vmid INTEGER`,
		},
	},
	{
		version: 19,
		name:    "add_sandbox_secrets",
		// One hashed secret per sandbox, issued at bootstrap. The metadata and
		// credential-proxy endpoints require it in addition to the source IP,
		// so a neighbor cannot spoof another sandbox's address (review F4).
		statements: []string{
			`CREATE TABLE IF NOT EXISTS sandbox_secrets (
				vmid INTEGER PRIMARY KEY,
				secret_hash TEXT NOT NULL,
				created_at TEXT NOT NULL,
				FOREIGN KEY(vmid) REFERENCES sandboxes(vmid) ON DELETE CASCADE
			)`,
		},
	},
	{
		version: 20,
		name:    "add_sandbox_lease_warned",
		// Set when a pre-expiry warning has been delivered for the current
		// lease term; cleared on lease renewal so a renewed lease warns again.
		statements: []string{
			`ALTER TABLE sandboxes ADD COLUMN lease_warned_at TEXT`,
		},
	},
	{
		version: 21,
		name:    "add_sandbox_static_ips",
		statements: []string{
			`CREATE TABLE sandbox_static_ips (
				ip TEXT PRIMARY KEY,
				vmid INTEGER NOT NULL REFERENCES sandboxes(vmid) ON DELETE CASCADE
			)`,
			`CREATE INDEX idx_sandbox_static_ips_vmid ON sandbox_static_ips(vmid)`,
		},
	},
	{version: 22, name: "remote_template_builds", statements: []string{
		`CREATE TABLE template_builds (id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, request_json TEXT NOT NULL, vmid INTEGER NOT NULL UNIQUE, state TEXT NOT NULL, record_json TEXT NOT NULL)`,
		`CREATE TABLE template_build_logs (build_id TEXT NOT NULL REFERENCES template_builds(id), sequence INTEGER NOT NULL, timestamp TEXT NOT NULL, stage TEXT NOT NULL, message TEXT NOT NULL, PRIMARY KEY(build_id, sequence))`,
		`CREATE TABLE template_manifests (vmid INTEGER PRIMARY KEY, build_id TEXT NOT NULL UNIQUE REFERENCES template_builds(id), record_json TEXT NOT NULL)`,
		`CREATE TABLE vmid_reservations (vmid INTEGER PRIMARY KEY, owner TEXT NOT NULL)`,
		`CREATE TRIGGER sandbox_template_reservation BEFORE INSERT ON sandboxes WHEN EXISTS(SELECT 1 FROM vmid_reservations WHERE vmid=NEW.vmid) BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: vmid_reservations.vmid'); END`,
		`CREATE INDEX template_builds_state ON template_builds(state)`,
		`CREATE UNIQUE INDEX template_builds_single_worker ON template_builds(state) WHERE state='RUNNING'`,
	}},
	{version: 23, name: "guest_recovery_identity", statements: []string{
		`CREATE TABLE guest_identities (vmid INTEGER PRIMARY KEY, incarnation TEXT NOT NULL UNIQUE, public_key TEXT UNIQUE, revoked INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO guest_identities(vmid,incarnation,revoked) SELECT vmid,lower(hex(randomblob(16))),CASE WHEN state='DESTROYED' THEN 1 ELSE 0 END FROM sandboxes`,
		`CREATE TABLE guest_challenges (id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, nonce_hash TEXT NOT NULL, generation TEXT NOT NULL, expires_at TEXT NOT NULL, consumed INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE guest_deliveries (incarnation TEXT NOT NULL, request_id TEXT NOT NULL, ciphertext BLOB NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY(incarnation,request_id))`,
		`CREATE TABLE guest_attempts (incarnation TEXT NOT NULL, job_id TEXT NOT NULL, attempt_id TEXT NOT NULL UNIQUE, started INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(incarnation,job_id))`,
		`ALTER TABLE bootstrap_tokens ADD COLUMN incarnation TEXT NOT NULL DEFAULT ''`,
		`UPDATE bootstrap_tokens SET incarnation=COALESCE((SELECT incarnation FROM guest_identities g WHERE g.vmid=bootstrap_tokens.vmid),'')`,
		`ALTER TABLE sandbox_secrets ADD COLUMN expires_at TEXT`,
		`CREATE TRIGGER guest_incarnation_insert AFTER INSERT ON sandboxes BEGIN
 DELETE FROM guest_challenges WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid);
 DELETE FROM guest_deliveries WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid);
 DELETE FROM guest_attempts WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid);
 DELETE FROM sandbox_secrets WHERE vmid=NEW.vmid;
 DELETE FROM bootstrap_tokens WHERE vmid=NEW.vmid;
 INSERT OR REPLACE INTO guest_identities(vmid,incarnation) VALUES(NEW.vmid,lower(hex(randomblob(16)))); END`,
		`CREATE TRIGGER guest_token_incarnation AFTER INSERT ON bootstrap_tokens BEGIN UPDATE bootstrap_tokens SET incarnation=COALESCE((SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid),'') WHERE token=NEW.token; END`,
		`CREATE TRIGGER guest_revoke_destroy AFTER UPDATE OF state ON sandboxes WHEN NEW.state='DESTROYED' BEGIN
 UPDATE guest_identities SET revoked=1,public_key=NULL WHERE vmid=NEW.vmid;
 DELETE FROM guest_challenges WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid);
 DELETE FROM guest_deliveries WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=NEW.vmid);
 DELETE FROM sandbox_secrets WHERE vmid=NEW.vmid;
 DELETE FROM bootstrap_tokens WHERE vmid=NEW.vmid; END`,
		`CREATE TRIGGER guest_revoke_delete BEFORE DELETE ON sandboxes BEGIN
 UPDATE guest_identities SET revoked=1,public_key=NULL WHERE vmid=OLD.vmid;
 DELETE FROM guest_challenges WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=OLD.vmid);
 DELETE FROM guest_deliveries WHERE incarnation IN (SELECT incarnation FROM guest_identities WHERE vmid=OLD.vmid);
 DELETE FROM sandbox_secrets WHERE vmid=OLD.vmid;
 DELETE FROM bootstrap_tokens WHERE vmid=OLD.vmid; END`,
	}},
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
	if len(m.statements) == 0 && m.goMigrate == nil {
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
	if m.goMigrate != nil {
		if err := m.goMigrate(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("go-migrate %d: %w", m.version, err)
		}
	}
	appliedAt := time.Now().UTC().Format(timeLayoutFixed)
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
