package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows")
	}

	t.Run("0600 ok", func(t *testing.T) {
		path := writeTempConfig(t, 0o600)
		warn, err := CheckConfigPermissions(path)
		assert.NoError(t, err)
		assert.Equal(t, "", warn)
	})

	t.Run("0640 warns", func(t *testing.T) {
		path := writeTempConfig(t, 0o640)
		warn, err := CheckConfigPermissions(path)
		assert.NoError(t, err)
		assert.Contains(t, warn, "group-readable")
	})

	t.Run("0644 fails", func(t *testing.T) {
		path := writeTempConfig(t, 0o644)
		_, err := CheckConfigPermissions(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must not be accessible by others")
	})

	t.Run("0620 fails", func(t *testing.T) {
		path := writeTempConfig(t, 0o620)
		_, err := CheckConfigPermissions(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "group-writable")
	})

	t.Run("0000 fails", func(t *testing.T) {
		path := writeTempConfig(t, 0o000)
		_, err := CheckConfigPermissions(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "readable by owner")
	})
}

func writeTempConfig(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	require.NoError(t, os.Chmod(path, mode))
	return path
}
