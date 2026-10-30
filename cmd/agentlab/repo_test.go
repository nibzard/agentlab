package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepoSnapshotJSONAndOfflineContext(t *testing.T) {
	useTempClientConfig(t)
	t.Setenv("AGENTLAB_CONTEXT", "missing-context")
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "Initial"}} {
		command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "code.txt"), []byte("local change"), 0600))
	opts, args, err := parseGlobal([]string{"--json", "repo", "snapshot", "--repo", root, "--out", t.TempDir()})
	require.NoError(t, err)
	read, write, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = original; read.Close(); write.Close() })
	require.NoError(t, dispatch(context.Background(), args, commonFlags{jsonOutput: opts.jsonOutput}))
	write.Close()
	os.Stdout = original
	var output bytes.Buffer
	_, err = output.ReadFrom(read)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, float64(1), result["file_count"])
	require.NotEmpty(t, result["source_revision"])
	require.NotEmpty(t, result["bundle_sha256"])
	require.NotEmpty(t, result["manifest_path"])
	require.Nil(t, result["files"])
}

func TestRepoResultSyntaxValidationDoesNotExportFiles(t *testing.T) {
	original := validateCommandOnly
	validateCommandOnly = true
	t.Cleanup(func() { validateCommandOnly = original })
	output := filepath.Join(t.TempDir(), "result-output")
	err := runRepoCommand(context.Background(), []string{"result", "--repo", "missing-repository", "--base-revision", "1111111111111111111111111111111111111111", "--out", output}, commonFlags{jsonOutput: true})
	require.ErrorIs(t, err, errCommandValidated)
	_, err = os.Stat(output)
	require.True(t, os.IsNotExist(err))
}
