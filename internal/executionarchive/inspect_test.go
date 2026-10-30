package executionarchive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/stretchr/testify/require"
)

func inspectArchive(t *testing.T, runner []byte, extra map[string][]byte) []byte {
	t.Helper()
	return inspectReportArchive(t, reportObject(), runner, extra)
}
func inspectReportArchive(t *testing.T, body map[string]any, runner []byte, extra map[string][]byte) []byte {
	t.Helper()
	report, err := json.Marshal(body)
	require.NoError(t, err)
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	write := func(name string, data []byte) {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}))
		_, err := archive.Write(data)
		require.NoError(t, err)
	}
	write("agent-runner.log", runner)
	write("report.json", report)
	for name, data := range extra {
		write(name, data)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return output.Bytes()
}
func inspect(t *testing.T, data []byte, options executionarchive.InspectOptions) (executionarchive.Inspection, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "execution.tar.gz")
	require.NoError(t, os.WriteFile(path, data, 0600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	sum := sha256.Sum256(data)
	return executionarchive.Inspect(context.Background(), file, expectation(), hex.EncodeToString(sum[:]), int64(len(data)), options)
}
func TestExecutionInspectionPaginatesUTF8AndEmptyLogs(t *testing.T) {
	log := []byte("a界🌍b\n")
	data := inspectArchive(t, log, nil)
	index, err := inspect(t, data, executionarchive.InspectOptions{})
	require.NoError(t, err)
	require.Nil(t, index.Chunk)
	require.Len(t, index.Entries, 2)
	require.Equal(t, "agent-runner.log", index.Entries[0].Path)
	var result string
	var offset int64
	for {
		page, err := inspect(t, data, executionarchive.InspectOptions{Path: "agent-runner.log", Offset: offset, Limit: 4})
		require.NoError(t, err)
		require.LessOrEqual(t, page.Chunk.NextOffset-offset, int64(4))
		result += page.Chunk.Text
		if page.Chunk.EOF {
			break
		}
		require.Greater(t, page.Chunk.NextOffset, offset)
		offset = page.Chunk.NextOffset
	}
	require.Equal(t, string(log), result)
	_, err = inspect(t, data, executionarchive.InspectOptions{Path: "agent-runner.log", Offset: 2, Limit: 4})
	require.ErrorIs(t, err, executionarchive.ErrInvalidOffset)
	empty, err := inspect(t, inspectArchive(t, nil, nil), executionarchive.InspectOptions{Path: "agent-runner.log"})
	require.NoError(t, err)
	require.True(t, empty.Chunk.EOF)
	require.Empty(t, empty.Chunk.Text)
}
func TestExecutionInspectionRejectsUnverifiedArchiveBeforeReturningText(t *testing.T) {
	for _, mode := range []string{"unsafe-later-entry", "credential-later-entry", "trailing-gzip", "changed-digest", "changed-size", "wrong-job"} {
		t.Run(mode, func(t *testing.T) {
			extra := map[string][]byte{}
			if mode == "unsafe-later-entry" {
				extra["../unsafe"] = []byte("unverified text")
			}
			if mode == "credential-later-entry" {
				extra["notes.txt"] = []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nprivate")
			}
			data := inspectArchive(t, []byte("must-not-be-returned"), extra)
			if mode == "trailing-gzip" {
				data = append(data, data...)
			}
			path := filepath.Join(t.TempDir(), "archive")
			require.NoError(t, os.WriteFile(path, data, 0600))
			file, err := os.Open(path)
			require.NoError(t, err)
			defer file.Close()
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:])
			size := int64(len(data))
			expected := expectation()
			if mode == "changed-digest" {
				digest = strings.Repeat("a", 64)
			}
			if mode == "changed-size" {
				size++
			}
			if mode == "wrong-job" {
				expected.JobID = "another-job"
			}
			result, err := executionarchive.Inspect(context.Background(), file, expected, digest, size, executionarchive.InspectOptions{Path: "agent-runner.log"})
			require.Error(t, err)
			require.Nil(t, result.Chunk)
			require.Empty(t, result.Entries)
		})
	}
}
func TestExecutionInspectionRejectsBinaryAndInvalidOffsets(t *testing.T) {
	data := inspectArchive(t, []byte{0xff, 0xfe}, nil)
	index, err := inspect(t, data, executionarchive.InspectOptions{})
	require.NoError(t, err)
	require.False(t, index.Entries[0].Text)
	_, err = inspect(t, data, executionarchive.InspectOptions{Path: "agent-runner.log"})
	require.ErrorIs(t, err, executionarchive.ErrEntryNotText)
	for _, options := range []executionarchive.InspectOptions{{Path: "../secret"}, {Path: "agent-runner.log", Offset: -1}, {Path: "agent-runner.log", Limit: 1}, {Path: "agent-runner.log", Limit: executionarchive.MaxChunkBytes + 1}, {Offset: 1}} {
		result, err := inspect(t, data, options)
		require.Error(t, err)
		require.Nil(t, result.Chunk)
	}
}

func TestExecutionInspectionBindsTestLogsToRequestedCommands(t *testing.T) {
	log := []byte("test report\n")
	sum := sha256.Sum256(log)
	body := reportObject()
	body["agent_exit_code"] = 0
	body["test_runs"] = []map[string]any{{"command": "make test", "state": "passed", "exit_code": 0, "duration_seconds": 1, "log_path": "tests/001.log", "log_sha256": hex.EncodeToString(sum[:])}}
	expected := expectation()
	expected.TestCommands = []string{"make test"}
	for _, changed := range []bool{false, true} {
		content := log
		if changed {
			content = []byte("other report\n")
		}
		data := inspectReportArchive(t, body, []byte("runner\n"), map[string][]byte{"tests/001.log": content})
		path := filepath.Join(t.TempDir(), "archive")
		require.NoError(t, os.WriteFile(path, data, 0600))
		file, err := os.Open(path)
		require.NoError(t, err)
		digest := sha256.Sum256(data)
		result, err := executionarchive.Inspect(context.Background(), file, expected, hex.EncodeToString(digest[:]), int64(len(data)), executionarchive.InspectOptions{Path: "tests/001.log"})
		require.NoError(t, file.Close())
		if changed {
			require.Error(t, err)
			require.Nil(t, result.Chunk)
		} else {
			require.NoError(t, err)
			require.Len(t, result.Entries, 3)
			require.Equal(t, string(log), result.Chunk.Text)
			require.Equal(t, "passed", result.Evidence.Tests)
		}
	}
}
