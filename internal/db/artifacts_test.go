package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateArtifact(t *testing.T) {
	ctx := context.Background()

	t.Run("success with all fields", func(t *testing.T) {
		store := openTestStore(t)

		// Create job first
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		vmid := 123
		artifact := Artifact{
			JobID:     "job-1",
			VMID:      &vmid,
			Name:      "test-output.txt",
			Path:      "artifacts/job-1/test-output.txt",
			SizeBytes: 1024,
			Sha256:    "a1b2c3d4e5f6",
			MIME:      "text/plain",
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))
	})

	t.Run("success with minimal fields", func(t *testing.T) {
		store := openTestStore(t)

		// Create job first
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test-output.txt",
			Path:      "artifacts/job-1/test-output.txt",
			SizeBytes: 1024,
			Sha256:    "a1b2c3d4e5f6",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))
	})

	t.Run("nil store", func(t *testing.T) {
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := (*Store)(nil).CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "db store is nil")
		assert.Equal(t, int64(0), id)
	})

	t.Run("nil db", func(t *testing.T) {
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := (&Store{}).CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "db store is nil")
		assert.Equal(t, int64(0), id)
	})

	t.Run("missing job id", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "job id is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("empty job id after trim", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "   ",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "job id is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("missing name", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact name is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("empty name after trim", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "   ",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact name is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("missing path", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact path is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("empty path after trim", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "   ",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact path is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("invalid size - zero", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 0,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact size must be positive")
		assert.Equal(t, int64(0), id)
	})

	t.Run("invalid size - negative", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: -100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact size must be positive")
		assert.Equal(t, int64(0), id)
	})

	t.Run("missing sha256", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact sha256 is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("empty sha256 after trim", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "   ",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.EqualError(t, err, "artifact sha256 is required")
		assert.Equal(t, int64(0), id)
	})

	t.Run("requires existing job", func(t *testing.T) {
		store := openTestStore(t)
		artifact := Artifact{
			JobID:     "nonexistent-job",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		assert.Error(t, err) // Foreign key constraint
		assert.Equal(t, int64(0), id)
	})

	t.Run("with existing job", func(t *testing.T) {
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

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))
	})

	t.Run("auto timestamps", func(t *testing.T) {
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

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, id, list[0].ID)
		assert.WithinDuration(t, time.Now().UTC(), list[0].CreatedAt, time.Second)
	})
}

func TestDeleteArtifact(t *testing.T) {
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

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)

		err = store.DeleteArtifact(ctx, id)
		require.NoError(t, err)

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		err := store.DeleteArtifact(ctx, 99999)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("invalid id - zero", func(t *testing.T) {
		store := openTestStore(t)
		err := store.DeleteArtifact(ctx, 0)
		assert.EqualError(t, err, "artifact id is required")
	})

	t.Run("invalid id - negative", func(t *testing.T) {
		store := openTestStore(t)
		err := store.DeleteArtifact(ctx, -1)
		assert.EqualError(t, err, "artifact id is required")
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).DeleteArtifact(ctx, 1)
		assert.EqualError(t, err, "db store is nil")
	})
}

func TestListArtifactsByJob(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
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

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("multiple artifacts ordered by created_at", func(t *testing.T) {
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

		artifact1 := Artifact{
			JobID:     "job-1",
			Name:      "file1.txt",
			Path:      "artifacts/file1.txt",
			SizeBytes: 100,
			Sha256:    "abc1",
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		artifact2 := Artifact{
			JobID:     "job-1",
			Name:      "file2.txt",
			Path:      "artifacts/file2.txt",
			SizeBytes: 200,
			Sha256:    "abc2",
			CreatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}
		_, err = store.CreateArtifact(ctx, artifact1)
		require.NoError(t, err)
		_, err = store.CreateArtifact(ctx, artifact2)
		require.NoError(t, err)

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 2)
		assert.Equal(t, "file1.txt", list[0].Name)
		assert.Equal(t, "file2.txt", list[1].Name)
	})

	t.Run("job not found", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListArtifactsByJob(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("empty job id after trim", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListArtifactsByJob(ctx, "   ")
		assert.EqualError(t, err, "job id is required")
		assert.Nil(t, list)
	})

	t.Run("nil store", func(t *testing.T) {
		list, err := (*Store)(nil).ListArtifactsByJob(ctx, "x")
		assert.EqualError(t, err, "db store is nil")
		assert.Nil(t, list)
	})
}

func TestListArtifactRetentionCandidates(t *testing.T) {
	ctx := context.Background()

	t.Run("empty list", func(t *testing.T) {
		store := openTestStore(t)
		list, err := store.ListArtifactRetentionCandidates(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("returns artifacts with job metadata", func(t *testing.T) {
		store := openTestStore(t)

		// Create job with sandbox
		vmid := 100
		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			SandboxVMID: &vmid,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		// Create sandbox
		sb := models.Sandbox{
			VMID:        100,
			Name:        "test-sandbox",
			Profile:     "default",
			State:       models.SandboxCompleted,
			CreatedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			LastUpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err = store.CreateSandbox(ctx, sb)
		require.NoError(t, err)

		// Create artifact
		artifact := Artifact{
			JobID:     "job-1",
			VMID:      &vmid,
			Name:      "test-output.txt",
			Path:      "artifacts/test-output.txt",
			SizeBytes: 1024,
			Sha256:    "a1b2c3d4e5f6",
			CreatedAt: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		}
		_, err = store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)

		list, err := store.ListArtifactRetentionCandidates(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 1)

		record := list[0]
		assert.Equal(t, "test-output.txt", record.Artifact.Name)
		assert.Equal(t, "job-1", record.Artifact.JobID)
		assert.Equal(t, "default", record.JobProfile)
		assert.Equal(t, models.JobCompleted, record.JobStatus)
		assert.Equal(t, 100, *record.Artifact.VMID)
		assert.Equal(t, 100, *record.SandboxVMID)
		assert.Equal(t, models.SandboxCompleted, record.SandboxState)
	})

	t.Run("artifact without sandbox", func(t *testing.T) {
		store := openTestStore(t)

		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test-output.txt",
			Path:      "artifacts/test-output.txt",
			SizeBytes: 1024,
			Sha256:    "a1b2c3d4e5f6",
			CreatedAt: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
		}
		_, err = store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)

		list, err := store.ListArtifactRetentionCandidates(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 1)

		record := list[0]
		assert.Nil(t, record.Artifact.VMID)
		assert.Nil(t, record.SandboxVMID)
		assert.Equal(t, models.SandboxState(""), record.SandboxState)
	})

	t.Run("nil store", func(t *testing.T) {
		list, err := (*Store)(nil).ListArtifactRetentionCandidates(ctx)
		assert.EqualError(t, err, "db store is nil")
		assert.Nil(t, list)
	})
}

func TestArtifactCascadeDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("artifacts deleted when job deleted", func(t *testing.T) {
		store := openTestStore(t)

		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test.txt",
			Path:      "artifacts/test.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)

		// Verify artifact exists
		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 1)

		// Delete job (should cascade to artifacts)
		_, err = store.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, "job-1")
		require.NoError(t, err)

		// Verify artifact is deleted
		list, err = store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Empty(t, list)

		// Direct delete should also fail
		err = store.DeleteArtifact(ctx, id)
		assert.Error(t, err)
	})
}

func TestArtifactSpecialCharacters(t *testing.T) {
	ctx := context.Background()

	t.Run("special characters in name", func(t *testing.T) {
		store := openTestStore(t)

		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test file with spaces & symbols!@#.txt",
			Path:      "artifacts/test file with spaces.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, "test file with spaces & symbols!@#.txt", list[0].Name)
	})

	t.Run("unicode in name", func(t *testing.T) {
		store := openTestStore(t)

		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		artifact := Artifact{
			JobID:     "job-1",
			Name:      "test-file-日本語-файл.txt",
			Path:      "artifacts/test-unicode.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, "test-file-日本語-файл.txt", list[0].Name)
	})

	t.Run("very long name", func(t *testing.T) {
		store := openTestStore(t)

		job := models.Job{
			ID:        "job-1",
			RepoURL:   "https://github.com/example/repo",
			Ref:       "main",
			Profile:   "default",
			Status:    models.JobCompleted,
			CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		err := store.CreateJob(ctx, job)
		require.NoError(t, err)

		longName := strings.Repeat("a", 1000) + ".txt"
		artifact := Artifact{
			JobID:     "job-1",
			Name:      longName,
			Path:      "artifacts/long.txt",
			SizeBytes: 100,
			Sha256:    "abc123",
		}
		id, err := store.CreateArtifact(ctx, artifact)
		require.NoError(t, err)
		assert.Greater(t, id, int64(0))

		list, err := store.ListArtifactsByJob(ctx, "job-1")
		require.NoError(t, err)
		assert.Len(t, list, 1)
		assert.Equal(t, longName, list[0].Name)
	})
}
