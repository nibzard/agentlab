package executionarchive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/stretchr/testify/require"
)

func testReport(t *testing.T, command string, code int) (map[string]any, executionarchive.Expected) {
	t.Helper()
	body := reportObject()
	expected := expectation()
	expected.TestCommands = []string{command}
	state := "passed"
	if code != 0 {
		state = "failed"
		body["status"] = "FAILED"
		body["exit_code"] = 1
		expected.Status = "FAILED"
	}
	if code == 124 {
		state = "timed_out"
	}
	digest := sha256.Sum256([]byte("test output\n"))
	body["agent_exit_code"] = 0
	body["test_runs"] = []map[string]any{{"command": command, "state": state, "exit_code": code, "duration_seconds": 1, "log_path": "tests/001.log", "log_sha256": hex.EncodeToString(digest[:])}}
	return body, expected
}
func testArchive(t *testing.T, body map[string]any, log string) []byte {
	t.Helper()
	report, err := json.Marshal(body)
	require.NoError(t, err)
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, data := range map[string][]byte{"report.json": report, "agent-runner.log": []byte("done\n"), "tests/001.log": []byte(log)} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return out.Bytes()
}
func TestRequestedEvidenceBindsCommandsLogsAndExitCodes(t *testing.T) {
	for _, code := range []int{0, 7, 124} {
		body, expected := testReport(t, "go test ./...", code)
		evidence, err := validateBytes(t, testArchive(t, body, "test output\n"), expected)
		require.NoError(t, err)
		require.Equal(t, 0, evidence.AgentExitCode)
		want := "passed"
		if code != 0 {
			want = "failed"
		}
		require.Equal(t, want, evidence.Tests)
		_, err = validateBytes(t, testArchive(t, body, "tampered output\n"), expected)
		require.ErrorContains(t, err, "test log")
		other := expected
		other.TestCommands = []string{"true"}
		_, err = validateBytes(t, testArchive(t, body, "test output\n"), other)
		require.ErrorContains(t, err, "test command")
		delete(body, "test_runs")
		data, _ := json.Marshal(body)
		_, err = executionarchive.ValidateReport(data, expected)
		require.ErrorContains(t, err, "missing")
	}
}
func TestRequestedEvidenceRejectsFalseVerdicts(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(b map[string]any) { b["test_runs"].([]map[string]any)[0]["state"] = "passed" },
		func(b map[string]any) { b["status"] = "COMPLETED"; b["exit_code"] = 0 },
		func(b map[string]any) { delete(b, "agent_exit_code") },
		func(b map[string]any) { b["test_runs"].([]map[string]any)[0]["log_path"] = "../outside" },
	} {
		body, expected := testReport(t, "go test ./...", 7)
		change(body)
		data, _ := json.Marshal(body)
		_, err := executionarchive.ValidateReport(data, expected)
		require.Error(t, err)
	}
	for _, commands := range [][]string{{""}, {"true\x00false"}, {strings.Repeat("a", 4097)}, make([]string, 17)} {
		require.Error(t, executionarchive.ValidateTestCommands(commands))
	}
}

func TestEmptyTestRunsRemainCompatibleWithSavedReceipt(t *testing.T) {
	body := reportObject()
	body["agent_exit_code"] = 0
	body["test_runs"] = []any{}
	data, err := json.Marshal(body)
	require.NoError(t, err)
	evidence, err := executionarchive.ValidateReport(data, expectation())
	require.NoError(t, err)
	encoded, err := json.Marshal(evidence)
	require.NoError(t, err)
	var saved executionarchive.Evidence
	require.NoError(t, json.Unmarshal(encoded, &saved))
	require.True(t, executionarchive.Equal(evidence, saved))
	require.Nil(t, evidence.TestRuns)
}
