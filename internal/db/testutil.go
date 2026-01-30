package db

import (
	"testing"

	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/require"
)

// openTestStore creates a test database in a temporary directory.
// The database is automatically closed and removed when the test completes.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := testutil.MkdirTempInDir(t, t.TempDir())
	store, err := Open(path + "/test.db")
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
	})
	return store
}
