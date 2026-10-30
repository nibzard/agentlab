package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type inspectFixture struct {
	platform string
	commands [][]string
	fail     string
}

func (f *inspectFixture) runArgs(_ context.Context, args []string, _ io.Reader) (string, error) {
	f.commands = append(f.commands, args)
	if strings.Join(args, " ") == f.fail {
		return "", errors.New("unavailable")
	}
	switch args[0] {
	case "uname":
		return f.platform, nil
	case "cat":
		return "0123456789abcdef0123456789abcdef\n", nil
	case "id":
		return "0\n", nil
	case "df":
		return "Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/test 99999999 100 50000000 1% /\n", nil
	default:
		return "", nil
	}
}

func TestSetupInspectStopsUnsupportedHostBeforeMutation(t *testing.T) {
	fixture := &inspectFixture{platform: "Darwin arm64"}
	_, err := inspectBootstrapHost(context.Background(), fixture)
	require.ErrorContains(t, err, "requires Linux")
	require.Len(t, fixture.commands, 1)
}

func TestSetupInspectUsesServerArchitecture(t *testing.T) {
	fixture := &inspectFixture{platform: "Linux aarch64"}
	host, err := inspectBootstrapHost(context.Background(), fixture)
	require.NoError(t, err)
	require.Equal(t, "arm64", host.Architecture)
	binaries, err := resolveBootstrapBinaries(t.TempDir(), bootstrapOptions{targetArch: host.Architecture, releaseURL: "https://example.com/release"})
	require.NoError(t, err)
	require.Equal(t, "https://example.com/release/agentlab_linux_arm64", binaries.agentlabURL)
	command := buildRemoteDownloadArgs("/tmp/bundle", binaries)
	require.Contains(t, strings.Join(command, " "), "agentlab_linux_arm64")
	require.NotContains(t, strings.Join(command, " "), "agentlab_linux_amd64")
	require.Equal(t, filepath.Base(binaries.agentlabURL), "agentlab_linux_arm64")
}

func TestSetupInspectMissingAccessIsBlocked(t *testing.T) {
	fixture := &inspectFixture{fail: "uname -sm"}
	_, err := inspectBootstrapHost(context.Background(), fixture)
	var blocked *blockedRequirement
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, "credentials_required", blocked.Code)
	require.Equal(t, "ssh_access", blocked.Requirement)
}
