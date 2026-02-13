package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestNewBootstrapSSHClientRejectsUnsafeHost(t *testing.T) {
	_, err := newBootstrapSSHClient(bootstrapOptions{host: "root@bad;rm -rf /"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh host")
}

func TestNewBootstrapSSHClientRejectsUnsafeUser(t *testing.T) {
	_, err := newBootstrapSSHClient(bootstrapOptions{host: "example.com", sshUser: "root;rm"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh user")
}

func TestNewBootstrapSSHClientAllowsIPv6(t *testing.T) {
	client, err := newBootstrapSSHClient(bootstrapOptions{host: "[::1]"})
	require.NoError(t, err)
	assert.Equal(t, "root@[::1]", client.target)
}

func TestNewBootstrapSSHClientRejectsPortInHost(t *testing.T) {
	_, err := newBootstrapSSHClient(bootstrapOptions{host: "example.com:2222"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh host")
}

func TestBuildRemoteDownloadArgsAvoidsInterpolation(t *testing.T) {
	binaries := bootstrapBinaries{
		agentlabURL:  "https://example.com/agentlab?sig=abc&x=y",
		agentlabdURL: "https://example.com/agentlabd?sig=def&x=z",
	}
	args := buildRemoteDownloadArgs("/tmp/agentlab-bootstrap", binaries)
	require.Len(t, args, 7)
	script := args[2]
	assert.False(t, strings.Contains(script, binaries.agentlabURL))
	assert.False(t, strings.Contains(script, binaries.agentlabdURL))
	assert.Equal(t, binaries.agentlabdURL, args[5])
	assert.Equal(t, binaries.agentlabURL, args[6])
}
