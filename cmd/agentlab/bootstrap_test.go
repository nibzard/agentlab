package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBootstrapSSHClientArgs(t *testing.T) {
	opts := bootstrapOptions{
		host:             "root@example.com",
		sshPort:          2222,
		identity:         "/tmp/id_ed25519",
		acceptNewHostKey: true,
	}
	client, err := newBootstrapSSHClient(opts)
	require.NoError(t, err)
	assert.Equal(t, "root@example.com", client.target)
	assert.Equal(t, []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-i", "/tmp/id_ed25519",
		"-p", "2222",
	}, client.args)
}

func TestWriteBootstrapClientConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := writeBootstrapClientConfig("http://example:8845", "secret")
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	cfg, ok, err := loadClientConfigFrom(path)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "http://example:8845", cfg.Endpoint)
	assert.Equal(t, "secret", cfg.Token)
}

func TestParseInitConnect(t *testing.T) {
	report := initReport{ConnectCommand: "agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token abc123"}
	data, err := json.Marshal(report)
	require.NoError(t, err)

	endpoint, token, err := parseInitConnect(string(data))
	require.NoError(t, err)
	assert.Equal(t, "http://host.tailnet.ts.net:8845", endpoint)
	assert.Equal(t, "abc123", token)
}

func TestResolveBootstrapBinariesUpload(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	agentlabPath := filepath.Join(dist, "agentlab_linux_amd64")
	agentlabdPath := filepath.Join(dist, "agentlabd_linux_amd64")
	require.NoError(t, os.WriteFile(agentlabPath, []byte("bin"), 0o755))
	require.NoError(t, os.WriteFile(agentlabdPath, []byte("bin"), 0o755))

	binaries, err := resolveBootstrapBinaries(root, bootstrapOptions{})
	require.NoError(t, err)
	assert.Equal(t, "upload", binaries.mode)
	assert.Equal(t, agentlabPath, binaries.agentlab)
	assert.Equal(t, agentlabdPath, binaries.agentlabd)
}

func TestResolveBootstrapBinariesDownload(t *testing.T) {
	root := t.TempDir()
	binaries, err := resolveBootstrapBinaries(root, bootstrapOptions{releaseURL: "https://example.com/release"})
	require.NoError(t, err)
	assert.Equal(t, "download", binaries.mode)
	assert.Equal(t, "https://example.com/release/agentlab_linux_amd64", binaries.agentlabURL)
	assert.Equal(t, "https://example.com/release/agentlabd_linux_amd64", binaries.agentlabdURL)
}

func TestResolveBootstrapBinariesUploadRequiresBoth(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "agentlab_linux_amd64"), []byte("bin"), 0o755))

	_, err := resolveBootstrapBinaries(root, bootstrapOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both agentlab and agentlabd binaries")
}
