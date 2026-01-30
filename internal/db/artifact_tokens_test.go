package db

import (
	"context"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Artifact Token Hash Tests

func TestHashArtifactToken(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		token := "test-artifact-token-12345"
		hash, err := HashArtifactToken(token)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		assert.Len(t, hash, 64) // SHA-256 hex digest
	})

	t.Run("empty token", func(t *testing.T) {
		hash, err := HashArtifactToken("")
		assert.EqualError(t, err, "token is required")
		assert.Empty(t, hash)
	})

	t.Run("whitespace only", func(t *testing.T) {
		hash, err := HashArtifactToken("   ")
		assert.EqualError(t, err, "token is required")
		assert.Empty(t, hash)
	})

	t.Run("trimmed whitespace", func(t *testing.T) {
		token1 := "artifact-token"
		token2 := "  artifact-token  "
		hash1, err := HashArtifactToken(token1)
		require.NoError(t, err)
		hash2, err := HashArtifactToken(token2)
		require.NoError(t, err)
		assert.Equal(t, hash1, hash2)
	})
}

// Artifact Token CRUD Tests

func TestCreateArtifactToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).CreateArtifactToken(ctx, "hash", "job-1", 1, time.Now())
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing hash", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateArtifactToken(ctx, "", "job-1", 100, time.Now())
		assert.EqualError(t, err, "token hash is required")
	})

	t.Run("missing job id", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateArtifactToken(ctx, "hash", "", 100, time.Now())
		assert.EqualError(t, err, "job id is required")
	})

	t.Run("invalid vmid - zero", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateArtifactToken(ctx, "hash", "job-1", 0, time.Now())
		assert.EqualError(t, err, "vmid must be positive")
	})

	t.Run("missing expires_at", func(t *testing.T) {
		store := openTestStore(t)
		err := store.CreateArtifactToken(ctx, "hash", "job-1", 100, time.Time{})
		assert.EqualError(t, err, "expires_at is required")
	})

	t.Run("requires existing job", func(t *testing.T) {
		store := openTestStore(t)
		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		err = store.CreateArtifactToken(ctx, hash, "nonexistent-job", 100, expires)
		assert.Error(t, err) // Foreign key constraint
	})
}

func TestGetArtifactToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)

		token, err := store.GetArtifactToken(ctx, hash)
		require.NoError(t, err)
		assert.Equal(t, hash, token.TokenHash)
		assert.Equal(t, "job-1", token.JobID)
		assert.Equal(t, 100, *token.VMID)
	})

	t.Run("not found", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetArtifactToken(ctx, "nonexistent-hash")
		assert.Error(t, err)
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := (*Store)(nil).GetArtifactToken(ctx, "hash")
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing hash", func(t *testing.T) {
		store := openTestStore(t)
		_, err := store.GetArtifactToken(ctx, "")
		assert.EqualError(t, err, "token hash is required")
	})
}

func TestTouchArtifactToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)

		// Touch the token
		err = store.TouchArtifactToken(ctx, hash, now)
		require.NoError(t, err)

		// Verify last_used_at was updated
		token, err := store.GetArtifactToken(ctx, hash)
		require.NoError(t, err)
		assert.Equal(t, now, token.LastUsedAt)
	})

	t.Run("token not found is no error", func(t *testing.T) {
		store := openTestStore(t)
		err := store.TouchArtifactToken(ctx, "nonexistent-hash", now)
		// No error for touching non-existent token
		require.NoError(t, err)
	})

	t.Run("nil store", func(t *testing.T) {
		err := (*Store)(nil).TouchArtifactToken(ctx, "hash", now)
		assert.EqualError(t, err, "db store is nil")
	})

	t.Run("missing hash", func(t *testing.T) {
		store := openTestStore(t)
		err := store.TouchArtifactToken(ctx, "", now)
		assert.EqualError(t, err, "token hash is required")
	})

	t.Run("auto now when zero", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)

		before := time.Now().UTC()
		err = store.TouchArtifactToken(ctx, hash, time.Time{})
		require.NoError(t, err)

		token, err := store.GetArtifactToken(ctx, hash)
		require.NoError(t, err)
		assert.True(t, token.LastUsedAt.After(before) || token.LastUsedAt.Equal(before))
	})
}

func TestArtifactTokenExpiration(t *testing.T) {
	ctx := context.Background()

	t.Run("expired token", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC) // In the past
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)

		// Token can still be retrieved
		token, err := store.GetArtifactToken(ctx, hash)
		require.NoError(t, err)
		assert.Equal(t, expires, token.ExpiresAt)
	})

	t.Run("future token", func(t *testing.T) {
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

		hash, err := HashArtifactToken("test-token")
		require.NoError(t, err)

		expires := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) // Far in the future
		err = store.CreateArtifactToken(ctx, hash, "job-1", 100, expires)
		require.NoError(t, err)

		token, err := store.GetArtifactToken(ctx, hash)
		require.NoError(t, err)
		assert.Equal(t, expires, token.ExpiresAt)
	})
}
