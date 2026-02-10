package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate(t *testing.T) {
	t.Run("fresh database applies all migrations", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify schema_migrations table
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 11, count) // We have 11 migrations

		// Verify version numbers
		rows, err := conn.Query("SELECT version FROM schema_migrations ORDER BY version")
		require.NoError(t, err)
		defer rows.Close()

		versions := []int{}
		for rows.Next() {
			var v int
			err = rows.Scan(&v)
			require.NoError(t, err)
			versions = append(versions, v)
		}
		assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, versions)
	})

	t.Run("idempotent - re-running is safe", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		// First migration
		err = Migrate(conn)
		require.NoError(t, err)

		// Second migration (should be no-op)
		err = Migrate(conn)
		require.NoError(t, err)

		// Verify only 11 migrations recorded
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 11, count)
	})

	t.Run("creates all core tables", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Check expected tables exist
		tables := []string{
			"sandboxes", "jobs", "profiles", "workspaces",
			"bootstrap_tokens", "events", "artifact_tokens", "artifacts", "exposures", "messages", "sessions", "workspace_snapshots",
		}

		for _, table := range tables {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "table %s should exist", table)
		}
	})

	t.Run("creates indexes", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Check some key indexes exist
		indexes := []string{
			"idx_sandboxes_state", "idx_jobs_status", "idx_jobs_workspace", "idx_jobs_session",
			"idx_workspaces_attached", "idx_artifacts_job", "idx_exposures_vmid", "idx_messages_scope", "idx_messages_ts",
			"idx_sessions_name", "idx_sessions_workspace", "idx_sessions_current_vmid", "idx_sessions_branch",
			"idx_workspace_snapshots_workspace", "idx_workspace_snapshots_created",
		}

		for _, index := range indexes {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "index %s should exist", index)
		}
	})

	t.Run("nil db", func(t *testing.T) {
		err := Migrate(nil)
		assert.EqualError(t, err, "db is nil")
	})
}

func TestMigrationVersion1(t *testing.T) {
	t.Run("init_core_tables creates expected schema", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify schema_migrations table structure
		var hasVersion, hasName, hasAppliedAt bool
		rows, err := conn.Query("PRAGMA table_info(schema_migrations)")
		require.NoError(t, err)
		defer rows.Close()

		for rows.Next() {
			var colID int
			var colName, colType string
			var colNotNull int
			var colDefault interface{}
			var colPK int
			err = rows.Scan(&colID, &colName, &colType, &colNotNull, &colDefault, &colPK)
			require.NoError(t, err)

			switch colName {
			case "version":
				hasVersion = true
			case "name":
				hasName = true
			case "applied_at":
				hasAppliedAt = true
			}
		}
		assert.True(t, hasVersion, "schema_migrations should have version column")
		assert.True(t, hasName, "schema_migrations should have name column")
		assert.True(t, hasAppliedAt, "schema_migrations should have applied_at column")
	})

	t.Run("sandboxes table structure", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify key columns exist
		columns := []string{
			"vmid", "name", "profile", "state", "ip",
			"workspace_id", "keepalive", "lease_expires_at", "last_used_at",
			"created_at", "updated_at", "meta_json",
		}

		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('sandboxes') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "sandboxes.%s column should exist", col)
		}
	})

	t.Run("jobs table structure", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify key columns exist
		columns := []string{
			"id", "repo_url", "ref", "profile", "status",
			"sandbox_vmid", "created_at", "updated_at", "result_json",
		}

		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "jobs.%s column should exist", col)
		}
	})
}

func TestMigrationVersion2(t *testing.T) {
	t.Run("adds job spec fields", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify new columns exist
		columns := []string{"task", "mode", "ttl_minutes", "keepalive"}

		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "jobs.%s column should exist", col)
		}
	})

	t.Run("alter table jobs preserves existing data", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		// Run only first migration
		for _, m := range migrations {
			if m.version == 1 {
				// Create schema_migrations table first
				_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
					version INTEGER PRIMARY KEY,
					name TEXT NOT NULL,
					applied_at TEXT NOT NULL
				)`)
				require.NoError(t, err)
				for _, stmt := range m.statements {
					_, err = conn.Exec(stmt)
					require.NoError(t, err)
				}
				_, err = conn.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, datetime('now'))", m.version, m.name)
				require.NoError(t, err)
				break
			}
		}

		// Insert a job
		_, err = conn.Exec("INSERT INTO jobs (id, repo_url, ref, profile, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))",
			"job-1", "https://github.com/example/repo", "main", "default", "QUEUED")
		require.NoError(t, err)

		// Run second migration
		err = Migrate(conn)
		require.NoError(t, err)

		// Verify job still exists
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE id = ?", "job-1").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestMigrationVersion8(t *testing.T) {
	t.Run("adds messages table", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		columns := []string{
			"id", "ts", "scope_type", "scope_id", "author", "kind", "text", "json",
		}

		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "messages.%s column should exist", col)
		}
	})
}

func TestMigrationVersion11(t *testing.T) {
	t.Run("adds workspace_snapshots table", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		columns := []string{"workspace_id", "name", "backend_ref", "created_at", "meta_json"}
		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('workspace_snapshots') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "workspace_snapshots.%s column should exist", col)
		}
	})

	t.Run("creates workspace snapshot indexes", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		indexes := []string{"idx_workspace_snapshots_workspace", "idx_workspace_snapshots_created"}
		for _, index := range indexes {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "index %s should exist", index)
		}
	})
}

func TestMigrationVersion3(t *testing.T) {
	t.Run("adds artifacts tables", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Verify artifact_tokens table
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='artifact_tokens'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Verify artifacts table
		err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='artifacts'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("artifact_tokens foreign key to jobs", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Try to insert artifact_token for non-existent job (should fail)
		_, err = conn.Exec("INSERT INTO artifact_tokens (token, job_id, vmid, expires_at, created_at) VALUES (?, ?, ?, datetime('now', '+1 hour'), datetime('now'))",
			"hash", "nonexistent-job", 100)
		assert.Error(t, err)
	})

	t.Run("artifacts foreign key to jobs with cascade delete", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		// Create a job
		_, err = conn.Exec("INSERT INTO jobs (id, repo_url, ref, profile, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))",
			"job-1", "https://github.com/example/repo", "main", "default", "QUEUED")
		require.NoError(t, err)

		// Create an artifact
		res, err := conn.Exec("INSERT INTO artifacts (job_id, vmid, name, path, size_bytes, sha256, created_at) VALUES (?, ?, ?, ?, ?, ?, datetime('now'))",
			"job-1", 100, "test.txt", "artifacts/test.txt", 100, "abc123")
		require.NoError(t, err)

		artifactID, _ := res.LastInsertId()

		// Delete job (should cascade to artifacts)
		_, err = conn.Exec("DELETE FROM jobs WHERE id = ?", "job-1")
		require.NoError(t, err)

		// Verify artifact was deleted
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM artifacts WHERE id = ?", artifactID).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestMigrationVersion4(t *testing.T) {
	t.Run("adds sandboxes last_used_at column", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('sandboxes') WHERE name='last_used_at'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestMigrationVersion6(t *testing.T) {
	t.Run("adds jobs workspace_id column", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name='workspace_id'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("creates jobs workspace index", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_jobs_workspace'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestMigrationVersion7(t *testing.T) {
	t.Run("adds workspace lease columns", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		columns := []string{"lease_owner", "lease_nonce", "lease_expires_at"}
		for _, col := range columns {
			var count int
			err = conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('workspaces') WHERE name=?", col).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "workspaces.%s column should exist", col)
		}
	})

	t.Run("creates workspace lease expiry index", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		err = Migrate(conn)
		require.NoError(t, err)

		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_workspaces_lease_expires'").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func TestPartialMigration(t *testing.T) {
	t.Run("applies only pending migrations", func(t *testing.T) {
		path := t.TempDir() + "/test.db"
		conn, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		defer conn.Close()

		// Manually apply only first migration
		for _, m := range migrations {
			if m.version == 1 {
				// Create schema_migrations table first
				_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
					version INTEGER PRIMARY KEY,
					name TEXT NOT NULL,
					applied_at TEXT NOT NULL
				)`)
				require.NoError(t, err)
				for _, stmt := range m.statements {
					_, err = conn.Exec(stmt)
					require.NoError(t, err)
				}
				_, err = conn.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, datetime('now'))", m.version, m.name)
				require.NoError(t, err)
				break
			}
		}

		// Run migrations - should apply 2, 3, 4, 5, 6, 7, 8, 9, 10, and 11
		err = Migrate(conn)
		require.NoError(t, err)

		// Verify all 11 migrations applied
		var count int
		err = conn.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 11, count)

		// Verify tables from migration 2 and 3 exist
		var tables int
		err = conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('artifact_tokens', 'artifacts')").Scan(&tables)
		require.NoError(t, err)
		assert.Equal(t, 2, tables)
	})
}

func TestMigrationValidation(t *testing.T) {
	// Note: validateMigrations is tested implicitly by TestMigrate
	// This test covers edge cases

	t.Run("all migrations have valid versions", func(t *testing.T) {
		// migrations is a package variable that should always be valid
		assert.Greater(t, len(migrations), 0)

		// Check versions are sequential
		for i, m := range migrations {
			assert.Equal(t, i+1, m.version, "migration %d should have version %d", i, i+1)
			assert.NotEmpty(t, m.name, "migration %d should have a name", m.version)
			assert.NotEmpty(t, m.statements, "migration %d should have statements", m.version)
		}
	})
}
