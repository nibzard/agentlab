package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/installassets"
	"github.com/stretchr/testify/require"
)

func TestClientResolvesPackagedAssetsOutsideCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := resolveBootstrapAssets("")
	require.NoError(t, err)
	require.True(t, hasBootstrapAssets(root))
	require.NoError(t, installassets.Verify(root))
	for _, name := range []string{"scripts/install_host.sh", "skills/agentlab/SKILL.md"} {
		_, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
	}
	_, err = resolveBootstrapAssets(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
