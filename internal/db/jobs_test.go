package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		// Verify job was created
		got, err := store.GetJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Equal(t, "job-1", got.ID)
		assert.Equal(t, "https://github.com/example/repo", got.RepoURL)
		assert.Equal(t, "main", got.Ref)
		assert.Equal(t, "default", got.Profile)
		assert.Equal(t, models.JobQueued, got.Status)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateJob(ctx, models.Job{ID: "x", RepoURL: "y", Ref: "z", Profile: "p", Status: models.JobQueued})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("nil db", func(t *testing.T) {
		err := (&Store{}).CreateJob(ctx, models.Job{ID: "x", RepoURL: "y", Ref: "z", Profile: "p", Status: models.JobQueued})
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing id", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			RepoURL: "https://github.com/example/repo",
			Ref:     "main",
			Profile: "default",
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job id is required")
	})

	t.Run("missing repo_url", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			Ref:     "main",
			Profile: "default",
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job repo_url is required")
	})

	t.Run("missing ref", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: "https://github.com/example/repo",
			Profile: "default",
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job ref is required")
	})

	t.Run("missing profile", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: "https://github.com/example/repo",
			Ref:     "main",
			Status:  models.JobQueued,
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job profile is required")
	})

	t.Run("missing status", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:      "job-1",
			RepoURL: "https://github.com/example/repo",
			Ref:     "main",
			Profile: "default",
		}
		err := store.CreateJob(ctx, job)
		assert.EqualError(t, err, "job status is required")
	})

	t.Run("duplicate id", func(t *testing.T) {
		store := openTestStore(t)
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.CreateJob(ctx, job)
		assert.Error(t, err)
	})

	t.Run("with optional fields", func(t *testing.T) {
		store := openTestStore(t)
		vmid := 123
		job := models.Job{
			ID:          "job-1",
			RepoURL:     "https://github.com/example/repo",
			Ref:         "main",
			Profile:     "default",
			Task:        "test-task",
			Mode:        "test-mode",
			TTLMinutes:  60,
			Keepalive:   true,
			Status:      models.JobQueued,
			SandboxVMID: &vmid,
			ResultJSON:  `{"output": "test"}`,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			UpdatedAt: before, // Should use this for updated_at if set
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			SandboxVMID: &vmid,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		oldJob := models.Job{
			ID:        "job-old",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "old",
			Profile:   "default",
			Status:    models.JobCompleted,
			SandboxVMID: &vmid,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, oldJob)
		require.NoError(t, err)

		// Create newer job
		newJob := models.Job{
			ID:        "job-new",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "new",
			Profile:   "default",
			Status:    models.JobRunning,
			SandboxVMID: &vmid,
			CreatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobQueued,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobRunning,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobRunning,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
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
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobRunning,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		err = store.UpdateJobResult(ctx, "job-1", "", "{}")
		assert.EqualError(t, err, "job status is required")
	})
}
