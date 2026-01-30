package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// openTestStore creates a test database in a temporary directory.
// The database is automatically closed and removed when the test completes.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		store.Close()
		os.Remove(path)
	})
	return store
}
