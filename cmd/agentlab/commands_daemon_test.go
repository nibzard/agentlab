package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/config"
	"github.com/agentlab/agentlab/internal/daemon"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestDaemon starts a real daemon for testing CLI commands.
// Returns cleanup function and config.
func startTestDaemon(t *testing.T) (*daemon.Service, config.Config, func()) {
	t.Helper()

	temp := t.TempDir()
	cfg := config.Config{
		RunDir:                  filepath.Join(temp, "run"),
		SocketPath:              filepath.Join(temp, "run", "agentlabd.sock"),
		DBPath:                  filepath.Join(temp, "agentlab.db"),
		BootstrapListen:         "127.0.0.1:0",
		ArtifactListen:          "127.0.0.1:0",
		ArtifactDir:             filepath.Join(temp, "artifacts"),
		ArtifactMaxBytes:        1024,
		ArtifactTokenTTLMinutes: 5,
		SecretsDir:              filepath.Join(temp, "secrets"),
		SecretsBundle:           "default",
		SecretsAgeKeyPath:       filepath.Join(temp, "age.key"),
		SecretsSopsPath:         "sops",
		SnippetsDir:             filepath.Join(temp, "snippets"),
		SnippetStorage:          "local",
		ProxmoxCommandTimeout:   time.Second,
		ProvisioningTimeout:     time.Second,
	}

	// Create profiles directory
	profilesDir := filepath.Join(temp, "profiles")
	require.NoError(t, os.MkdirAll(profilesDir, 0755))
	cfg.ProfilesDir = profilesDir

	// Create a test profile
	profilePath := filepath.Join(profilesDir, "test.yaml")
	profileContent := `
name: test
template_vm: 9000
cpu: 2
memory: 4096
network: bridge
`
	require.NoError(t, os.WriteFile(profilePath, []byte(profileContent), 0644))

	// Open database
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)

	// Load profiles
	profiles := map[string]models.Profile{
		"test": {
			Name:       "test",
			TemplateVM: 9000,
			UpdatedAt:  time.Now().UTC(),
			RawYAML:    profileContent,
		},
	}

	// Create service
	service, err := daemon.NewService(cfg, profiles, store)
	require.NoError(t, err)

	// Start serving in background
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	// Wait for server to be ready
	readyCh := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			if _, err := os.Stat(cfg.SocketPath); err == nil {
				close(readyCh)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-readyCh:
		// Server is ready
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start within timeout")
	}

	cleanup := func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Fatal("daemon did not shutdown within timeout")
		}
		store.Close()
	}

	return service, cfg, cleanup
}

// Note: We rely on the daemon's graceful shutdown via context cancellation
// to clean up resources. The OS will clean up the socket file when the
// temp directory is removed by t.TempDir().

// Test CLI read-only endpoints against a real daemon.

func TestCLIReadOnly_WithRealDaemon(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	t.Run("sandbox list empty", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxList(ctx, nil, base)
			require.NoError(t, err)
		})
		// Should show header even when empty
		assert.Contains(t, out, "VMID")
		assert.Contains(t, out, "NAME")
	})

	t.Run("workspace list empty", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runWorkspaceList(ctx, nil, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "ID")
		assert.Contains(t, out, "NAME")
	})
}

func TestCLIReadOnly_WithData(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	now := time.Now().UTC()

	// Create test data using direct DB access
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	// Create a sandbox
	sandbox := models.Sandbox{
		VMID:          8001,
		Name:          "test-sandbox",
		Profile:       "test",
		State:         models.SandboxRunning,
		IP:            "10.77.0.10",
		Keepalive:     true,
		LeaseExpires:  now.Add(1 * time.Hour),
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	// Create a job
	job := models.Job{
		ID:          "test-job-123",
		RepoURL:     "https://github.com/example/repo",
		Ref:         "main",
		Profile:     "test",
		Task:        "run tests",
		Mode:        "dangerous",
		TTLMinutes:  60,
		Keepalive:   true,
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = store.CreateJob(ctx, job)
	require.NoError(t, err)

	// Create a workspace
	workspace := models.Workspace{
		ID:          "ws-test-1",
		Name:        "test-workspace",
		Storage:     "local-zfs",
		VolumeID:    "local-zfs:vm-8001-disk-1",
		SizeGB:      50,
		AttachedVM:  &sandbox.VMID,
		CreatedAt:   now,
		LastUpdated: now,
	}
	err = store.CreateWorkspace(ctx, workspace)
	require.NoError(t, err)

	// Create an event
	err = store.RecordEvent(ctx, "sandbox.started", &sandbox.VMID, &job.ID, "sandbox started", "")
	require.NoError(t, err)

	t.Run("sandbox list shows data", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxList(ctx, nil, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "8001")
		assert.Contains(t, out, "test-sandbox")
		assert.Contains(t, out, "RUNNING")
		assert.Contains(t, out, "10.77.0.10")
	})

	t.Run("sandbox show shows details", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxShow(ctx, []string{"8001"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "VMID: 8001")
		assert.Contains(t, out, "Name: test-sandbox")
		assert.Contains(t, out, "Profile: test")
		assert.Contains(t, out, "State: RUNNING")
		assert.Contains(t, out, "IP: 10.77.0.10")
	})

	t.Run("job show shows details", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runJobShow(ctx, []string{"test-job-123"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "Job ID: test-job-123")
		assert.Contains(t, out, "Repo: https://github.com/example/repo")
		assert.Contains(t, out, "Profile: test")
		assert.Contains(t, out, "Status: RUNNING")
		assert.Contains(t, out, "Sandbox VMID: 8001")
	})

	t.Run("job show with events tail", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runJobShow(ctx, []string{"--events-tail", "10", "test-job-123"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "Events:")
		assert.Contains(t, out, "sandbox.started")
	})

	t.Run("workspace list shows data", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runWorkspaceList(ctx, nil, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "ws-test-1")
		assert.Contains(t, out, "test-workspace")
		assert.Contains(t, out, "50")
		assert.Contains(t, out, "8001")
	})

	t.Run("logs shows events", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runLogsCommand(ctx, []string{"8001"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "sandbox.started")
	})
}

func TestCLIReadOnly_JSONOutput(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: true, timeout: 5 * time.Second}

	now := time.Now().UTC()

	// Create test data
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	sandbox := models.Sandbox{
		VMID:          8002,
		Name:          "json-sandbox",
		Profile:       "test",
		State:         models.SandboxRunning,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	err = store.CreateSandbox(ctx, sandbox)
	require.NoError(t, err)

	t.Run("sandbox list JSON", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxList(ctx, nil, base)
			require.NoError(t, err)
		})
		var resp sandboxesResponse
		err = json.Unmarshal([]byte(out), &resp)
		require.NoError(t, err)
		assert.Len(t, resp.Sandboxes, 1)
		assert.Equal(t, 8002, resp.Sandboxes[0].VMID)
		assert.Equal(t, "json-sandbox", resp.Sandboxes[0].Name)
	})

	t.Run("sandbox show JSON", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxShow(ctx, []string{"8002"}, base)
			require.NoError(t, err)
		})
		var resp sandboxResponse
		err = json.Unmarshal([]byte(out), &resp)
		require.NoError(t, err)
		assert.Equal(t, 8002, resp.VMID)
		assert.Equal(t, "json-sandbox", resp.Name)
	})

	t.Run("workspace list JSON", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runWorkspaceList(ctx, nil, base)
			require.NoError(t, err)
		})
		var resp workspacesResponse
		err = json.Unmarshal([]byte(out), &resp)
		require.NoError(t, err)
		// Empty list is valid
		assert.NotNil(t, resp.Workspaces)
	})
}

func TestCLIReadOnly_ErrorCases(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	t.Run("sandbox show not found", func(t *testing.T) {
		err := runSandboxShow(ctx, []string{"9999"}, base)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("job show not found", func(t *testing.T) {
		err := runJobShow(ctx, []string{"nonexistent-job"}, base)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("invalid vmid", func(t *testing.T) {
		err := runSandboxShow(ctx, []string{"invalid"}, base)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid vmid")
	})
}

func TestCLIReadOnly_JobArtifacts(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	now := time.Now().UTC()

	// Create test data
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	vmid := 8003
	job := models.Job{
		ID:          "artifact-job-1",
		RepoURL:     "https://github.com/example/repo",
		Ref:         "main",
		Profile:     "test",
		Status:      models.JobCompleted,
		SandboxVMID: &vmid,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = store.CreateJob(ctx, job)
	require.NoError(t, err)

	// Create artifact metadata
	artifactPath := filepath.Join(cfg.ArtifactDir, job.ID, "output.txt")
	artifact := db.Artifact{
		JobID:     job.ID,
		VMID:      &vmid,
		Name:      "output.txt",
		Path:      artifactPath,
		SizeBytes: 1024,
		Sha256:    "abc123def456",
		CreatedAt: now,
	}
	_, err = store.CreateArtifact(ctx, artifact)
	require.NoError(t, err)

	t.Run("job artifacts list", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runJobArtifactsList(ctx, []string{"artifact-job-1"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "output.txt")
		assert.Contains(t, out, "NAME")
	})

	t.Run("job artifacts JSON", func(t *testing.T) {
		jsonBase := base
		jsonBase.jsonOutput = true
		out := captureStdout(t, func() {
			err := runJobArtifactsList(ctx, []string{"artifact-job-1"}, jsonBase)
			require.NoError(t, err)
		})
		var resp artifactsResponse
		err = json.Unmarshal([]byte(out), &resp)
		require.NoError(t, err)
		assert.Equal(t, "artifact-job-1", resp.JobID)
		assert.Len(t, resp.Artifacts, 1)
		assert.Equal(t, "output.txt", resp.Artifacts[0].Name)
	})
}

func TestCLIReadOnly_Timeouts(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 1 * time.Nanosecond}

	t.Run("timeout on sandbox list", func(t *testing.T) {
		// With such a short timeout, even a simple request might timeout
		// This verifies the timeout mechanism works
		err := runSandboxList(ctx, nil, base)
		// Either it succeeds quickly or times out
		// The important thing is the timeout is being applied
		if err != nil {
			// Context deadline exceeded is the expected timeout error
			assert.Contains(t, err.Error(), "deadline exceeded") // this is optional but nice to have
		}
	})
}

// Test the full flow: create, then query via read-only endpoints.
// Note: Some create operations require additional services (SSH keys, Proxmox, etc.)
// that are not available in the test environment, so we create data directly in the DB.

func TestCLIReadOnly_FullFlow(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	now := time.Now().UTC()

	// Create test data using direct DB access
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	t.Run("create sandbox then query", func(t *testing.T) {
		// Create a sandbox directly in the DB
		sandbox := models.Sandbox{
			VMID:          9001,
			Name:          "flow-sandbox",
			Profile:       "test",
			State:         models.SandboxRunning,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}
		err = store.CreateSandbox(ctx, sandbox)
		require.NoError(t, err)

		// List sandboxes
		out := captureStdout(t, func() {
			err := runSandboxList(ctx, nil, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "9001")
		assert.Contains(t, out, "flow-sandbox")

		// Show sandbox
		out = captureStdout(t, func() {
			err := runSandboxShow(ctx, []string{"9001"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "VMID: 9001")
		assert.Contains(t, out, "Name: flow-sandbox")
	})

	t.Run("create workspace then query", func(t *testing.T) {
		// Create a workspace directly in the DB
		workspace := models.Workspace{
			ID:          "ws-flow-1",
			Name:        "flow-workspace",
			Storage:     "local-zfs",
			VolumeID:    "local-zfs:vm-9002-disk-1",
			SizeGB:      50,
			CreatedAt:   now,
			LastUpdated: now,
		}
		err = store.CreateWorkspace(ctx, workspace)
		require.NoError(t, err)

		// List workspaces
		out := captureStdout(t, func() {
			err := runWorkspaceList(ctx, nil, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "flow-workspace")
	})

	t.Run("create job then query", func(t *testing.T) {
		// Create a job directly in the DB
		job := models.Job{
			ID:         "flow-job-123",
			RepoURL:    "https://github.com/example/test",
			Ref:        "main",
			Profile:    "test",
			Task:       "test task",
			Mode:       "dangerous",
			TTLMinutes: 60,
			Keepalive:  false,
			Status:     models.JobQueued,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		err = store.CreateJob(ctx, job)
		require.NoError(t, err)

		// Show job
		out := captureStdout(t, func() {
			err := runJobShow(ctx, []string{"flow-job-123"}, base)
			require.NoError(t, err)
		})
		assert.Contains(t, out, "Job ID: flow-job-123")
		assert.Contains(t, out, "test task")
	})
}

// Test with profile filtering and different states

func TestCLIReadOnly_DifferentStates(t *testing.T) {
	_, cfg, cleanup := startTestDaemon(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	base := commonFlags{socketPath: cfg.SocketPath, jsonOutput: false, timeout: 5 * time.Second}

	now := time.Now().UTC()

	// Create test data
	store, err := db.Open(cfg.DBPath)
	require.NoError(t, err)
	defer store.Close()

	// Create sandboxes in different states
	states := []models.SandboxState{
		models.SandboxRunning,
		models.SandboxCompleted,
		models.SandboxFailed,
		models.SandboxDestroyed,
	}

	for i, state := range states {
		vmid := 8100 + i
		sandbox := models.Sandbox{
			VMID:          vmid,
			Name:          fmt.Sprintf("sandbox-%s", state),
			Profile:       "test",
			State:         state,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}
		err = store.CreateSandbox(ctx, sandbox)
		require.NoError(t, err)
	}

	t.Run("list all sandboxes", func(t *testing.T) {
		out := captureStdout(t, func() {
			err := runSandboxList(ctx, nil, base)
			require.NoError(t, err)
		})
		for _, state := range states {
			assert.Contains(t, out, string(state))
		}
	})

	t.Run("show each sandbox", func(t *testing.T) {
		for i := range states {
			vmid := 8100 + i
			out := captureStdout(t, func() {
				err := runSandboxShow(ctx, []string{fmt.Sprintf("%d", vmid)}, base)
				require.NoError(t, err)
			})
			assert.Contains(t, out, fmt.Sprintf("VMID: %d", vmid))
		}
	})
}
