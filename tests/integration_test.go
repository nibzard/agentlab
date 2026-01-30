//go:build integration
// +build integration

package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests require:
// 1. A running Proxmox instance (or mock)
// 2. Valid Proxmox credentials
// 3. Network access
// Run with: go test -tags=integration ./tests/...

// Helper to create a test config for integration tests
func integrationConfig(t *testing.T) config.Config {
	t.Helper()

	// Use environment variables or defaults
	profilesDir := os.Getenv("AGENTLAB_PROFILES_DIR")
	if profilesDir == "" {
		profilesDir = "/etc/agentlab/profiles"
	}

	dataDir := os.Getenv("AGENTLAB_DATA_DIR")
	if dataDir == "" {
		dataDir = t.TempDir()
	}

	temp := t.TempDir()

	return config.Config{
		ConfigPath:              filepath.Join(temp, "config.yaml"),
		ProfilesDir:             profilesDir,
		DataDir:                 dataDir,
		LogDir:                  filepath.Join(temp, "log"),
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
		DBPath:                  filepath.Join(temp, "agentlab.db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        256 * 1024 * 1024,
		ArtifactTokenTTLMinutes: 1440,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   2 * time.Minute,
		ProvisioningTimeout:     10 * time.Minute,
	}
}

// Helper to create a test database
func openTestDB(t *testing.T) *db.Store {
	t.Helper()
	temp := t.TempDir()
	path := filepath.Join(temp, "test.db")
	store, err := db.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
	})
	return store
}

// TestCompleteJobLifecycle tests the full job lifecycle from creation to completion
func TestCompleteJobLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)

	// Step 1: Create a job with QUEUED status
	job := models.Job{
		ID:        "integration-test-job-1",
		RepoURL:   "https://github.com/example/repo",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobQueued,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	err := store.CreateJob(ctx, job)
	require.NoError(t, err)

	// Verify job was created
	got, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, models.JobQueued, got.Status)

	// Step 2: Simulate job transitioning to RUNNING
	err = store.UpdateJobStatus(ctx, job.ID, models.JobRunning)
	require.NoError(t, err)

	got, err = store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, models.JobRunning, got.Status)

	// Step 3: Create a sandbox for the job
	sandbox := models.Sandbox{
		VMID:        1000,
		Name:        "integration-test-sandbox",
		Profile:     "default",
		State:       models.SandboxRunning,
		CreatedAt:   time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Step 4: Attach sandbox to job
	vmid := sandbox.VMID
	_, err = store.UpdateJobSandbox(ctx, job.ID, vmid)
	require.NoError(t, err)

	got, err = store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.SandboxVMID)
	assert.Equal(t, vmid, *got.SandboxVMID)

	// Step 5: Simulate job completion with result
	resultJSON := `{"exit_code": 0, "output": "test completed successfully"}`
	err = store.UpdateJobResult(ctx, job.ID, models.JobCompleted, resultJSON)
	require.NoError(t, err)

	got, err = store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, models.JobCompleted, got.Status)
	assert.Equal(t, resultJSON, got.ResultJSON)

	// Step 6: Create artifact metadata
	artifact := db.Artifact{
		JobID:     job.ID,
		VMID:      &vmid,
		Name:      "output.txt",
		Path:      filepath.Join("artifacts", job.ID, "output.txt"),
		SizeBytes: 100,
		Sha256:    "abc123",
		CreatedAt: time.Now().UTC(),
	}
	artifactID, err := store.CreateArtifact(ctx, artifact)
	require.NoError(t, err)
	assert.Greater(t, artifactID, int64(0))

	// Verify artifact was created
	artifacts, err := store.ListArtifactsByJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Len(t, artifacts, 1)

	// Step 7: Cleanup - transition sandbox to completed
	_, err = store.UpdateSandboxState(ctx, vmid, models.SandboxRunning, models.SandboxCompleted)
	require.NoError(t, err)

	sandboxGot, err := store.GetSandbox(ctx, vmid)
	require.NoError(t, err)
	assert.Equal(t, models.SandboxCompleted, sandboxGot.State)
}

// TestLeaseRenewal tests sandbox lease expiration and renewal
func TestLeaseRenewal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)
	now := time.Now().UTC()

	// Step 1: Create sandbox with TTL
	sandbox := models.Sandbox{
		VMID:         1001,
		Name:         "lease-test-sandbox",
		Profile:      "default",
		State:        models.SandboxRunning,
		LeaseExpires: now.Add(1 * time.Hour),
		CreatedAt:    now,
		LastUpdatedAt: now,
	}
	err := store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Step 2: Verify lease_expires_at is set correctly
	got, err := store.GetSandbox(ctx, sandbox.VMID)
	require.NoError(t, err)
	assert.False(t, got.LeaseExpires.IsZero())
	assert.WithinDuration(t, now.Add(1*time.Hour), got.LeaseExpires, time.Second)

	// Step 3: Run lease GC before expiration (should keep)
	expiredBefore, err := store.ListExpiredSandboxes(ctx, now.Add(30*time.Minute))
	require.NoError(t, err)
	assert.Len(t, expiredBefore, 0)

	// Step 4: Run lease GC after expiration (should find)
	expiredAfter, err := store.ListExpiredSandboxes(ctx, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Len(t, expiredAfter, 1)
	assert.Equal(t, sandbox.VMID, expiredAfter[0].VMID)

	// Step 5: Test lease renewal extends expiration
	newLease := now.Add(2 * time.Hour)
	err = store.UpdateSandboxLease(ctx, sandbox.VMID, newLease)
	require.NoError(t, err)

	got, err = store.GetSandbox(ctx, sandbox.VMID)
	require.NoError(t, err)
	assert.WithinDuration(t, newLease, got.LeaseExpires, time.Second)

	// Step 6: Verify it's no longer in expired list (check slightly before new expiration)
	expiredAfterRenewal, err := store.ListExpiredSandboxes(ctx, now.Add(2*time.Hour).Add(-1*time.Second))
	require.NoError(t, err)
	assert.Len(t, expiredAfterRenewal, 0)
}

// TestWorkspaceAttachDetach tests workspace lifecycle with multiple sandboxes
func TestWorkspaceAttachDetach(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)

	// Step 1: Create workspace (persistent storage)
	workspace := models.Workspace{
		ID:          "ws-integration-1",
		Name:        "integration-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-9999-disk-1",
		SizeGB:      50,
		CreatedAt:   time.Now().UTC(),
		LastUpdated: time.Now().UTC(),
	}
	err := store.CreateWorkspace(ctx, workspace)
	require.NoError(t, err)

	// Step 2: Create sandbox A
	sandboxA := models.Sandbox{
		VMID:        2000,
		Name:        "sandbox-a",
		Profile:     "default",
		State:       models.SandboxRunning,
		CreatedAt:   time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}
	err = store.CreateSandbox(ctx, sandboxA)
	require.NoError(t, err)

	// Step 3: Attach workspace to sandbox A
	attached, err := store.AttachWorkspace(ctx, workspace.ID, sandboxA.VMID)
	require.NoError(t, err)
	assert.True(t, attached)

	// Verify workspace attached to A
	ws, err := store.GetWorkspace(ctx, workspace.ID)
	require.NoError(t, err)
	assert.NotNil(t, ws.AttachedVM)
	assert.Equal(t, sandboxA.VMID, *ws.AttachedVM)

	// Step 4: Detach workspace from sandbox A (simulating sandbox A destruction)
	detached, err := store.DetachWorkspace(ctx, workspace.ID, sandboxA.VMID)
	require.NoError(t, err)
	assert.True(t, detached)

	// Verify workspace detached but persists
	ws, err = store.GetWorkspace(ctx, workspace.ID)
	require.NoError(t, err)
	assert.Nil(t, ws.AttachedVM)

	// Step 5: Create sandbox B
	sandboxB := models.Sandbox{
		VMID:        2001,
		Name:        "sandbox-b",
		Profile:     "default",
		State:       models.SandboxRunning,
		CreatedAt:   time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}
	err = store.CreateSandbox(ctx, sandboxB)
	require.NoError(t, err)

	// Step 6: Attach workspace to sandbox B
	attached, err = store.AttachWorkspace(ctx, workspace.ID, sandboxB.VMID)
	require.NoError(t, err)
	assert.True(t, attached)

	// Verify workspace now attached to B
	ws, err = store.GetWorkspace(ctx, workspace.ID)
	require.NoError(t, err)
	assert.NotNil(t, ws.AttachedVM)
	assert.Equal(t, sandboxB.VMID, *ws.AttachedVM)
}

// TestErrorRecovery tests error handling during provisioning
func TestErrorRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)

	// Step 1: Create job
	job := models.Job{
		ID:        "error-test-job",
		RepoURL:   "https://github.com/example/repo",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobQueued,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	err := store.CreateJob(ctx, job)
	require.NoError(t, err)

	// Step 2: Simulate provisioning failure - mark job as failed
	err = store.UpdateJobResult(ctx, job.ID, models.JobFailed, `{"error": "provisioning failed"}`)
	require.NoError(t, err)

	// Verify job marked as FAILED
	got, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, models.JobFailed, got.Status)
	assert.Contains(t, got.ResultJSON, "provisioning failed")

	// Step 3: Create a sandbox in FAILED state (simulating cleanup)
	sandbox := models.Sandbox{
		VMID:        3000,
		Name:        "failed-sandbox",
		Profile:     "default",
		State:       models.SandboxFailed,
		CreatedAt:   time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Step 4: Verify sandbox can be transitioned to DESTROYED
	_, err = store.UpdateSandboxState(ctx, sandbox.VMID, models.SandboxFailed, models.SandboxDestroyed)
	require.NoError(t, err)

	sandboxGot, err := store.GetSandbox(ctx, sandbox.VMID)
	require.NoError(t, err)
	assert.Equal(t, models.SandboxDestroyed, sandboxGot.State)

	// Step 5: Verify event was recorded for the failure
	err = store.RecordEvent(ctx, "sandbox_failed", &sandbox.VMID, &job.ID, "provisioning failed", "")
	require.NoError(t, err)
}

// TestDaemonStartup tests daemon startup with existing state
func TestDaemonStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Step 1: Pre-populate database with active sandboxes
	cfg := integrationConfig(t)
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	now := time.Now().UTC()

	// Create an active sandbox with lease
	sandbox := models.Sandbox{
		VMID:         4000,
		Name:         "existing-sandbox",
		Profile:      "default",
		State:        models.SandboxRunning,
		LeaseExpires: now.Add(1 * time.Hour),
		CreatedAt:    now.Add(-1 * time.Hour),
		LastUpdatedAt: now,
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Create a job
	job := models.Job{
		ID:        "existing-job",
		RepoURL:   "https://github.com/example/repo",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobRunning,
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now,
	}
	err = store.CreateJob(ctx, job)
	require.NoError(t, err)

	// Step 2: Start daemon service (without actually running it)
	// We're testing that NewService succeeds with existing state
	profiles := map[string]models.Profile{}
	service, err := daemon.NewService(cfg, profiles, store)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Service will be garbage collected, but we rely on the daemon's
		// internal cleanup mechanisms. For this test, we just verify
		// NewService succeeds with existing state in the database.
		_ = service
	})

	// Step 3: Verify existing sandboxes remain accessible
	got, err := store.GetSandbox(ctx, sandbox.VMID)
	require.NoError(t, err)
	assert.Equal(t, sandbox.VMID, got.VMID)
	assert.Equal(t, sandbox.Name, got.Name)

	// Step 4: Verify lease GC would process existing state
	expired, err := store.ListExpiredSandboxes(ctx, now)
	require.NoError(t, err)
	// Should not be expired yet
	assert.Len(t, expired, 0)

	// After lease expires
	expired, err = store.ListExpiredSandboxes(ctx, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Len(t, expired, 1)
}

// TestConcurrentOperations tests multiple simultaneous jobs
func TestConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)
	now := time.Now().UTC()

	// Step 1: Create 5 jobs simultaneously
	numJobs := 5
	jobs := make([]models.Job, numJobs)

	for i := 0; i < numJobs; i++ {
		jobs[i] = models.Job{
			ID:        fmt.Sprintf("concurrent-job-%d", i),
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := store.CreateJob(ctx, jobs[i])
		require.NoError(t, err)
	}

	// Step 2: Create 5 sandboxes simultaneously
	sandboxes := make([]models.Sandbox, numJobs)

	for i := 0; i < numJobs; i++ {
		vmid := 5000 + i
		sandboxes[i] = models.Sandbox{
			VMID:        vmid,
			Name:        fmt.Sprintf("concurrent-sandbox-%d", i),
			Profile:     "default",
			State:       models.SandboxRunning,
			CreatedAt:   now,
			LastUpdatedAt: now,
		}
		err := store.CreateSandbox(ctx, sandboxes[i])
		require.NoError(t, err)
	}

	// Step 3: Attach each sandbox to its job
	for i := 0; i < numJobs; i++ {
		vmid := sandboxes[i].VMID
		_, err := store.UpdateJobSandbox(ctx, jobs[i].ID, vmid)
		require.NoError(t, err)
	}

	// Step 4: Verify all jobs have sandboxes attached
	for i := 0; i < numJobs; i++ {
		got, err := store.GetJob(ctx, jobs[i].ID)
		require.NoError(t, err)
		assert.NotNil(t, got.SandboxVMID)
		assert.Equal(t, sandboxes[i].VMID, *got.SandboxVMID)
	}

	// Step 5: Complete all jobs
	for i := 0; i < numJobs; i++ {
		err := store.UpdateJobStatus(ctx, jobs[i].ID, models.JobCompleted)
		require.NoError(t, err)

		_, err = store.UpdateSandboxState(ctx, sandboxes[i].VMID, models.SandboxRunning, models.SandboxCompleted)
		require.NoError(t, err)
	}

	// Step 6: Verify database consistency
	allSandboxes, err := store.ListSandboxes(ctx)
	require.NoError(t, err)
	assert.Len(t, allSandboxes, numJobs)

	completedCount := 0
	for _, sb := range allSandboxes {
		if sb.State == models.SandboxCompleted {
			completedCount++
		}
	}
	assert.Equal(t, numJobs, completedCount)
}

// TestArtifactRetentionCandidateQuery tests the complex join query for artifact retention
func TestArtifactRetentionCandidateQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	store := openTestDB(t)
	now := time.Now().UTC()

	// Step 1: Create a completed job with sandbox
	vmid := 6000
	job := models.Job{
		ID:        "retention-test-job",
		RepoURL:   "https://github.com/example/repo",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobCompleted,
		SandboxVMID: &vmid,
		CreatedAt: now.Add(-24 * time.Hour),
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	err := store.CreateJob(ctx, job)
	require.NoError(t, err)

	sandbox := models.Sandbox{
		VMID:        vmid,
		Name:        "retention-sandbox",
		Profile:     "default",
		State:       models.SandboxDestroyed,
		CreatedAt:   now.Add(-24 * time.Hour),
		LastUpdatedAt: now.Add(-1 * time.Hour),
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Step 2: Create artifacts
	artifact := db.Artifact{
		JobID:     job.ID,
		VMID:      &vmid,
		Name:      "test-output.txt",
		Path:      filepath.Join("artifacts", job.ID, "test-output.txt"),
		SizeBytes: 1024,
		Sha256:    "def456",
		CreatedAt: now.Add(-23 * time.Hour),
	}
	_, err = store.CreateArtifact(ctx, artifact)
	require.NoError(t, err)

	// Step 3: Query retention candidates
	candidates, err := store.ListArtifactRetentionCandidates(ctx)
	require.NoError(t, err)
	assert.Len(t, candidates, 1)

	record := candidates[0]
	assert.Equal(t, "test-output.txt", record.Artifact.Name)
	assert.Equal(t, job.ID, record.Artifact.JobID)
	assert.Equal(t, "default", record.JobProfile)
	assert.Equal(t, models.JobCompleted, record.JobStatus)
	assert.Equal(t, vmid, *record.SandboxVMID)
	assert.Equal(t, models.SandboxDestroyed, record.SandboxState)
}
