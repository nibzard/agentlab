// ABOUTME: Tests for per-sandbox endpoint secret storage (review F4).
package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/agentlab/agentlab/internal/models"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashSandboxSecret(t *testing.T) {
	t.Run("deterministic and hex-encoded", func(t *testing.T) {
		hash1, err := HashSandboxSecret("secret-one")
		require.NoError(t, err)
		hash2, err := HashSandboxSecret("secret-one")
		require.NoError(t, err)
		assert.Equal(t, hash1, hash2)
		assert.Len(t, hash1, 64)
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		hash1, err := HashSandboxSecret("secret-one")
		require.NoError(t, err)
		hash2, err := HashSandboxSecret("  secret-one\t")
		require.NoError(t, err)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("distinct secrets hash differently", func(t *testing.T) {
		hash1, err := HashSandboxSecret("secret-one")
		require.NoError(t, err)
		hash2, err := HashSandboxSecret("secret-two")
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("rejects empty values", func(t *testing.T) {
		for _, in := range []string{"", "   "} {
			_, err := HashSandboxSecret(in)
			assert.Error(t, err, "input %q", in)
		}
	})
}

func TestUpsertAndGetSandboxSecret(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedSandboxForSecret(t, store, 7001)

	t.Run("insert stores the hash", func(t *testing.T) {
		hash, err := HashSandboxSecret("first-secret")
		require.NoError(t, err)
		require.NoError(t, store.UpsertSandboxSecret(ctx, 7001, hash))
		got, err := store.GetSandboxSecretHash(ctx, 7001)
		require.NoError(t, err)
		assert.Equal(t, hash, got)
	})

	t.Run("second upsert rotates the hash", func(t *testing.T) {
		first, err := HashSandboxSecret("first-secret")
		require.NoError(t, err)
		second, err := HashSandboxSecret("second-secret")
		require.NoError(t, err)
		require.NoError(t, store.UpsertSandboxSecret(ctx, 7001, second))
		got, err := store.GetSandboxSecretHash(ctx, 7001)
		require.NoError(t, err)
		assert.Equal(t, second, got)
		assert.NotEqual(t, first, got)
	})

	t.Run("missing row returns sql.ErrNoRows", func(t *testing.T) {
		_, err := store.GetSandboxSecretHash(ctx, 7999)
		assert.Equal(t, sql.ErrNoRows, err)
	})

	t.Run("delete removes the row", func(t *testing.T) {
		require.NoError(t, store.DeleteSandboxSecret(ctx, 7001))
		_, err := store.GetSandboxSecretHash(ctx, 7001)
		assert.Equal(t, sql.ErrNoRows, err)
		// Delete of an absent row is not an error.
		require.NoError(t, store.DeleteSandboxSecret(ctx, 7001))
	})
}

func TestUpsertSandboxSecretValidation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	seedSandboxForSecret(t, store, 7002)

	err := (*Store)(nil).UpsertSandboxSecret(ctx, 7002, "hash")
	assert.Error(t, err)
	err = store.UpsertSandboxSecret(ctx, 0, "hash")
	assert.Error(t, err)
	err = store.UpsertSandboxSecret(ctx, -1, "hash")
	assert.Error(t, err)
	err = store.UpsertSandboxSecret(ctx, 7002, "")
	assert.Error(t, err)
	err = store.UpsertSandboxSecret(ctx, 7002, "   ")
	assert.Error(t, err)
}

func TestGetSandboxSecretHashValidation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	_, err := (*Store)(nil).GetSandboxSecretHash(ctx, 1)
	assert.Error(t, err)
	_, err = store.GetSandboxSecretHash(ctx, 0)
	assert.Error(t, err)
}

func seedSandboxForSecret(t *testing.T, store *Store, vmid int) {
	t.Helper()
	sb := testutil.NewTestSandbox(testutil.SandboxOpts{
		VMID:    vmid,
		Name:    "secret-test-sandbox",
		Profile: testutil.TestProfile,
		State:   models.SandboxRunning,
	})
	require.NoError(t, store.CreateSandbox(context.Background(), sb))
}
