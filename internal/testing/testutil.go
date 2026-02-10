// ABOUTME: Package testing provides shared test utilities and helper functions for agentlab.
//
// This package contains test helpers, factory functions for creating test data,
// and assertion utilities that promote consistent testing patterns across
// the AgentLab codebase.
//
// Key utilities:
//   - Model factories: NewTestJob, NewTestSandbox, NewTestWorkspace
//   - Test helpers: TempFile, OpenTestDB, AssertJSONEqual
//   - Test constants: FixedTime, TestRepoURL, TestProfile
//
// The package is designed to work with github.com/stretchr/testify for
// assertions and follows Go testing best practices.
package testing

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentlab/agentlab/internal/models"
)

// FixedTime is a fixed timestamp for deterministic tests.
//
// Using a fixed time ensures tests produce consistent results regardless of
// when they run. Use this as the default time for test model creation.
var FixedTime = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

// Common test constants used across the test suite.
//
// These constants provide consistent default values for test data,
// making tests more readable and reducing duplication.
const (
	TestRepoURL     = "https://github.com/example/repo"
	TestProfile     = "default"
	TestRef         = "main"
	TestVMID        = 100
	TestVMIDAlt     = 200
	TestWorkspaceID = "ws-test-1"
)

// AssertJSONEqual asserts that two JSON values are semantically equal.
//
// This helper marshals both values to JSON and then compares the resulting
// JSON objects semantically, ignoring differences in whitespace and key order.
//
// Useful for comparing API responses or configuration structures.
func AssertJSONEqual(t *testing.T, want, got any, msgAndArgs ...interface{}) {
	t.Helper()
	wantBytes, err := json.Marshal(want)
	require.NoError(t, err, "failed to marshal 'want' to JSON")
	gotBytes, err := json.Marshal(got)
	require.NoError(t, err, "failed to marshal 'got' to JSON")

	var wantAny, gotAny any
	require.NoError(t, json.Unmarshal(wantBytes, &wantAny), "failed to unmarshal 'want'")
	require.NoError(t, json.Unmarshal(gotBytes, &gotAny), "failed to unmarshal 'got'")

	assert.Equal(t, wantAny, gotAny, msgAndArgs...)
}

// TempFile creates a temporary file with the given content and returns its path.
//
// The file is created in the test's temporary directory and automatically
// cleaned up when the test completes. Uses t.Helper() for correct line numbers.
//
// Returns the absolute path to the created file.
func TempFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "testfile")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err, "failed to write temp file")
	return path
}

// MkdirTempInDir creates a temporary directory under the given parent directory.
//
// Unlike t.TempDir(), which doesn't allow specifying the parent, this function
// creates a temporary directory as a subdirectory of parentDir. The directory
// is automatically cleaned up when the test completes.
//
// Returns the path to the created directory.
func MkdirTempInDir(t *testing.T, parentDir string) string {
	t.Helper()
	path, err := os.MkdirTemp(parentDir, "testdir*")
	require.NoError(t, err, "failed to create temp dir")
	t.Cleanup(func() {
		_ = os.RemoveAll(path)
	})
	return path
}

// ParseTime parses an RFC3339 timestamp.
//
// This is a convenience wrapper around time.Parse that uses t.Helper()
// for correct line numbers in test failures.
//
// Returns the parsed time or fails the test if parsing fails.
func ParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err, "failed to parse time %q", s)
	return ts
}

// RequireNoError asserts that err is nil, with a more descriptive message.
//
// This is a thin wrapper around require.NoError that adds t.Helper()
// for correct line numbers in test failures.
func RequireNoError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	require.NoError(t, err, msgAndArgs...)
}

// RequireEqual asserts that two values are equal, with a more descriptive message.
//
// This is a thin wrapper around require.Equal that adds t.Helper()
// for correct line numbers in test failures.
func RequireEqual(t *testing.T, expected, actual any, msgAndArgs ...interface{}) {
	t.Helper()
	require.Equal(t, expected, actual, msgAndArgs...)
}

// ============================================================================
// Model Factory Functions
// ============================================================================

// JobOpts holds optional parameters for creating test jobs.
//
// Used with NewTestJob to create test job data with specific values.
// Empty fields use sensible defaults defined in NewTestJob.
type JobOpts struct {
	ID          string
	RepoURL     string
	Ref         string
	Profile     string
	Task        string
	Mode        string
	TTLMinutes  int
	Keepalive   bool
	WorkspaceID *string
	SessionID   *string
	Status      models.JobStatus
	SandboxVMID *int
	ResultJSON  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewTestJob creates a test job with default values, applying optional overrides.
//
// This factory function creates valid Job structs for testing, filling in
// required fields with sensible defaults. Any field in opts can be set to
// override the default.
//
// Example:
//
//	job := NewTestJob(testing.JobOpts{
//	    Status: models.JobRunning,
//	    Task:   "run-tests",
//	})
func NewTestJob(opts JobOpts) models.Job {
	if opts.ID == "" {
		opts.ID = "job-test-1"
	}
	if opts.RepoURL == "" {
		opts.RepoURL = TestRepoURL
	}
	if opts.Ref == "" {
		opts.Ref = TestRef
	}
	if opts.Profile == "" {
		opts.Profile = TestProfile
	}
	if opts.Status == "" {
		opts.Status = models.JobQueued
	}
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = FixedTime
	}
	if opts.UpdatedAt.IsZero() {
		opts.UpdatedAt = FixedTime
	}

	return models.Job{
		ID:          opts.ID,
		RepoURL:     opts.RepoURL,
		Ref:         opts.Ref,
		Profile:     opts.Profile,
		Task:        opts.Task,
		Mode:        opts.Mode,
		TTLMinutes:  opts.TTLMinutes,
		Keepalive:   opts.Keepalive,
		WorkspaceID: opts.WorkspaceID,
		SessionID:   opts.SessionID,
		Status:      opts.Status,
		SandboxVMID: opts.SandboxVMID,
		ResultJSON:  opts.ResultJSON,
		CreatedAt:   opts.CreatedAt,
		UpdatedAt:   opts.UpdatedAt,
	}
}

// SandboxOpts holds optional parameters for creating test sandboxes.
//
// Used with NewTestSandbox to create test sandbox data with specific values.
// Empty fields use sensible defaults defined in NewTestSandbox.
type SandboxOpts struct {
	VMID          int
	Name          string
	Profile       string
	WorkspaceID   *string
	State         models.SandboxState
	IP            string
	Keepalive     bool
	LeaseExpires  time.Time
	LastUsedAt    time.Time
	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

// NewTestSandbox creates a test sandbox with default values, applying optional overrides.
//
// This factory function creates valid Sandbox structs for testing, filling in
// required fields with sensible defaults. Any field in opts can be set to
// override the default.
func NewTestSandbox(opts SandboxOpts) models.Sandbox {
	if opts.VMID == 0 {
		opts.VMID = TestVMID
	}
	if opts.Name == "" {
		opts.Name = "sandbox-test-1"
	}
	if opts.Profile == "" {
		opts.Profile = TestProfile
	}
	if opts.State == "" {
		opts.State = models.SandboxRequested
	}
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = FixedTime
	}
	if opts.LastUpdatedAt.IsZero() {
		opts.LastUpdatedAt = FixedTime
	}

	return models.Sandbox{
		VMID:          opts.VMID,
		Name:          opts.Name,
		Profile:       opts.Profile,
		WorkspaceID:   opts.WorkspaceID,
		State:         opts.State,
		IP:            opts.IP,
		Keepalive:     opts.Keepalive,
		LeaseExpires:  opts.LeaseExpires,
		LastUsedAt:    opts.LastUsedAt,
		CreatedAt:     opts.CreatedAt,
		LastUpdatedAt: opts.LastUpdatedAt,
	}
}

// WorkspaceOpts holds optional parameters for creating test workspaces.
type WorkspaceOpts struct {
	ID          string
	Name        string
	SizeGB      int
	Storage     string
	VolumeID    string
	AttachedVM  *int
	CreatedAt   time.Time
	LastUpdated time.Time
}

// NewTestWorkspace creates a test workspace with default values, applying optional overrides.
func NewTestWorkspace(opts WorkspaceOpts) models.Workspace {
	if opts.ID == "" {
		opts.ID = TestWorkspaceID
	}
	if opts.Name == "" {
		opts.Name = "workspace-test-1"
	}
	if opts.SizeGB == 0 {
		opts.SizeGB = 50
	}
	if opts.Storage == "" {
		opts.Storage = "local-zfs"
	}
	if opts.VolumeID == "" {
		opts.VolumeID = "local-zfs:vm-100-disk-1"
	}
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = FixedTime
	}
	if opts.LastUpdated.IsZero() {
		opts.LastUpdated = FixedTime
	}

	return models.Workspace{
		ID:          opts.ID,
		Name:        opts.Name,
		SizeGB:      opts.SizeGB,
		Storage:     opts.Storage,
		VolumeID:    opts.VolumeID,
		AttachedVM:  opts.AttachedVM,
		CreatedAt:   opts.CreatedAt,
		LastUpdated: opts.LastUpdated,
	}
}

// ProfileOpts holds optional parameters for creating test profiles.
type ProfileOpts struct {
	Name       string
	TemplateVM int
	RawYAML    string
	UpdatedAt  time.Time
}

// NewTestProfile creates a test profile with default values, applying optional overrides.
func NewTestProfile(opts ProfileOpts) models.Profile {
	if opts.Name == "" {
		opts.Name = TestProfile
	}
	if opts.TemplateVM == 0 {
		opts.TemplateVM = 100
	}
	if opts.RawYAML == "" {
		opts.RawYAML = "name: default\nresources:\n  cpu: 2\n  memory: 2048\n"
	}
	if opts.UpdatedAt.IsZero() {
		opts.UpdatedAt = FixedTime
	}

	return models.Profile{
		Name:       opts.Name,
		TemplateVM: opts.TemplateVM,
		RawYAML:    opts.RawYAML,
		UpdatedAt:  opts.UpdatedAt,
	}
}

// ============================================================================
// Database Test Helpers
// ============================================================================

// OpenTestDB opens a test SQLite database in a temporary directory.
// The database is automatically closed and removed when the test completes.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err, "failed to open test database")
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

// RequireRowsAffected asserts that the expected number of rows were affected.
func RequireRowsAffected(t *testing.T, res sql.Result, expected int64) {
	t.Helper()
	n, err := res.RowsAffected()
	require.NoError(t, err, "failed to get rows affected")
	require.Equal(t, expected, n, "rows affected mismatch")
}

// RequireNoRows asserts that no rows exist in the table for the given query.
func RequireNoRows(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	var count int
	err := db.QueryRow(query, args...).Scan(&count)
	require.NoError(t, err, "failed to query rows")
	require.Equal(t, 0, count, "expected no rows")
}
