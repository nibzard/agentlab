package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		// Verify job was created
		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, "job-1", got.ID)
		assert.Equal(t, testutil.TestRepoURL, got.RepoURL)
		assert.Equal(t, testutil.TestRef, got.Ref)
		assert.Equal(t, testutil.TestProfile, got.Profile)
		assert.Equal(t, models.JobQueued, got.Status)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateJob(ctx, testutil.NewTestJob(testutil.JobOpts{ID: "x", Ref: "z"}))
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		err := (&Store{}).CreateJob(ctx, testutil.NewTestJob(testutil.JobOpts{ID: "x"}))
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing id", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			RepoURL:  testutil.TestRepoURL,
			Ref:      testutil.TestRef,
			Profile:  testutil.TestProfile,
			Status:   models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job id is required")
	})

	t.Run("missing repo_url", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			Ref:     testutil.TestRef,
			Profile: testutil.TestProfile,
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job repo_url is required")
	})

	t.Run("missing ref", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: testutil.TestRepoURL,
			Profile: testutil.TestProfile,
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job ref is required")
	})

	t.Run("missing profile", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: testutil.TestRepoURL,
			Ref:     testutil.TestRef,
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job profile is required")
	})

	t.Run("missing status", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: testutil.TestRepoURL,
			Ref:     testutil.TestRef,
			Profile: testutil.TestProfile,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job status is required")
	})

	t.Run("duplicate id", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.CreateJob(ctx, job)
		assert.Error(t, err)
	})

	t.Run("with optional fields", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 123
		job := testutil.NewTestJob(testutil.JobOpts{
			ID:          "job-1",
			Task:        "test-task",
			Mode:        "test-mode",
			TTLMinutes:  60,
			Keepalive:   true,
			SandboxVMID: &vmid,
			ResultJSON:  `{"output": "test"}`,
		})
		// Set optional fields that NewTestJob doesn't handle
		job.Task = "test-task"
		job.Mode = "test-mode"
		job.TTLMinutes = 60
		job.Keepalive = true
		job.SandboxVMID = &vmid
		job.ResultJSON = `{"output": "test"}`

		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, "test-task", got.Task)
		assert.Equal(t, "test-mode", got.Mode)
		assert.Equal(t, 60, got.TTLMinutes)
		assert.True(t, got.Keepalive)
		assert.Equal(t, 123, *got.SandboxVMID)
		assert.Equal(t, `{"output": "test"}`, got.ResultJSON)
	})

	t.Run("auto timestamps", func(t *testing.T) {
		store := openTestStore(t)
		before := time.Now().UTC()
		job := models.Job{
			ID:        "job-1",
			RepoURL:   testutil.TestRepoURL,
			Ref:       testutil.TestRef,
			Profile:   testutil.TestProfile,
			Status:    models.JobQueued,
			UpdatedAt: before,
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().UTC(), got.CreatedAt, time.Second)
		assert.Equal(t, before, got.UpdatedAt)
	})
}

func TestGetJob(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, "job-1", got.ID)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetJob(ctx, "nonexistent")
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetJob(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		_, err := (&Store{}).GetJob(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestGetJobBySandboxVMID(t *testing.T) {
	ctx := context.Background()

	t.Run("exists", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 123
		job := testutil.NewTestJob(testutil.JobOpts{
			ID:        "job-1",
			SandboxVMID: &vmid,
		})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		got, err := store.GetJobBySandboxVMID(ctx, 123)
		require.NoError(t, err)
		assert.Equal(t, "job-1", got.ID)
		assert.Equal(t, 123, *got.SandboxVMID)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetJobBySandboxVMID(ctx, 999)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("invalid vmid", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetJobBySandboxVMID(ctx, 0)
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("negative vmid", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetJobBySandboxVMID(ctx, -1)
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("most recent job first", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 123

		// Create older job
		oldJob := testutil.NewTestJob(testutil.JobOpts{
			ID:        "job-old",
			Ref:       "old",
			Status:    models.JobCompleted,
			SandboxVMID: &vmid,
		})
		err := store.CreateJob(ctx, oldJob)
		require.NoError(t, err)

		// Create newer job
		newJob := testutil.NewTestJob(testutil.JobOpts{
			ID:        "job-new",
			Ref:       "new",
			Status:    models.JobRunning,
			SandboxVMID: &vmid,
			CreatedAt: testutil.FixedTime.Add(time.Hour * 24),
			UpdatedAt: testutil.FixedTime.Add(time.Hour * 24),
		})
		err = store.CreateJob(ctx, newJob)
		require.NoError(t, err)

		got, err := store.GetJobBySandboxVMID(ctx, 123)
		require.NoError(t, err)
		assert.Equal(t, "job-new", got.ID)
	})
}

func TestUpdateJobSandbox(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		updated, err := store.UpdateJobSandbox(ctx, "job-1", 456)
		require.NoError(t, err)
		assert.True(t, updated)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, 456, *got.SandboxVMID)
	})

	t.Run("job not found", func(t *testing.T) {
		store := openTestStore(t)
		updated, err := store.UpdateJobSandbox(ctx, "nonexistent", 456)
		require.NoError(t, err)
		assert.False(t, updated)
	})

	t.Run("nil store", func(t *testing.T) {
		updated, err := (*Store)(nil).UpdateJobSandbox(ctx, "x", 1)
		assert.EqualError(t, err, "db store is nil")
		assert.False(t, updated)
	})

	t.Run("missing job id", func(t *testing.T) {
		store := openTestStore(t)
		updated, err := store.UpdateJobSandbox(ctx, "", 456)
		assert.EqualError(t, err, "job id is required")
		assert.False(t, updated)
	})

	t.Run("invalid vmid", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		_, err = store.UpdateJobSandbox(ctx, "job-1", 0)
		assert.EqualError(t, err, "vmid must be positive")
	})
}

func TestUpdateJobStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.UpdateJobStatus(ctx, "job-1", models.JobRunning)
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, models.JobRunning, got.Status)
		assert.WithinDuration(t, time.Now().UTC(), got.UpdatedAt, time.Second)
	})

	t.Run("job not found", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateJobStatus(ctx, "nonexistent", models.JobRunning)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).UpdateJobStatus(ctx, "x", models.JobRunning)
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing job id", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateJobStatus(ctx, "", models.JobRunning)
		assert.EqualError(t, err, "job id is required")
	})

	t.Run("missing status", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{ID: "job-1"})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.UpdateJobStatus(ctx, "job-1", "")
		assert.EqualError(t, err, "job status is required")
	})
}

func TestUpdateJobResult(t *testing.T) {
	ctx := context.Background()

	t.Run("success with result", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{
			ID:     "job-1",
			Status: models.JobRunning,
		})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		result := `{"exit_code": 0, "output": "success"}`
		err = store.UpdateJobResult(ctx, "job-1", models.JobCompleted, result)
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, models.JobCompleted, got.Status)
		assert.Equal(t, result, got.ResultJSON)
	})

	t.Run("success without result", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{
			ID:     "job-1",
			Status: models.JobRunning,
		})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.UpdateJobResult(ctx, "job-1", models.JobFailed, "")
		require.NoError(t, err)

		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, models.JobFailed, got.Status)
		assert.Equal(t, "", got.ResultJSON)
	})

	t.Run("job not found", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateJobResult(ctx, "nonexistent", models.JobCompleted, "{}")
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).UpdateJobResult(ctx, "x", models.JobCompleted, "{}")
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing job id", func(t *testing.T) {
		store := openTestStore(t)
		err := store.UpdateJobResult(ctx, "", models.JobCompleted, "{}")
		assert.EqualError(t, err, "job id is required")
	})

	t.Run("missing status", func(t *testing.T) {
		store := openTestStore(t)
		job := testutil.NewTestJob(testutil.JobOpts{
			ID:     "job-1",
			Status: models.JobRunning,
		})
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.UpdateJobResult(ctx, "job-1", "", "{}")
		assert.EqualError(t, err, "job status is required")
	})
}
