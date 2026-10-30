package executionarchive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/executionarchive"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/require"
)

func expectation() executionarchive.Expected {
	return executionarchive.Expected{JobID: "job-a", SourceID: strings.Repeat("a", 64), SourceRevision: strings.Repeat("b", 40), ManifestSHA256: strings.Repeat("c", 64), Status: "COMPLETED"}
}

func validateBytes(t *testing.T, data []byte, expected executionarchive.Expected) (executionarchive.Evidence, error) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "execution.tar.gz")
	require.NoError(t, os.WriteFile(filename, data, 0600))
	return executionarchive.Validate(context.Background(), filename, expected)
}

func TestExecutionEvidenceIsGuestReportedAndNotATestVerdict(t *testing.T) {
	expected := expectation()
	data := testutil.ExecutionArchive(t, expected)
	evidence, err := validateBytes(t, data, expected)
	require.NoError(t, err)
	require.Equal(t, executionarchive.Evidence{State: "guest_reported", AgentExitCode: 0, DurationSeconds: 1, Tests: "not_reported"}, evidence)
	for _, change := range []func(*executionarchive.Expected){
		func(e *executionarchive.Expected) { e.JobID = "other" },
		func(e *executionarchive.Expected) { e.SourceID = "other" },
		func(e *executionarchive.Expected) { e.SourceRevision = "other" },
		func(e *executionarchive.Expected) { e.ManifestSHA256 = "other" },
		func(e *executionarchive.Expected) { e.Status = "FAILED" },
	} {
		other := expected
		change(&other)
		_, err := validateBytes(t, data, other)
		require.Error(t, err)
	}
}

func reportObject() map[string]any {
	e := expectation()
	return map[string]any{"schema_version": 1, "job_id": e.JobID, "source_id": e.SourceID, "source_revision": e.SourceRevision, "status": e.Status, "exit_code": 0, "duration_seconds": 1, "repository_result": map[string]string{"manifest_sha256": e.ManifestSHA256}}
}

func TestReportRejectsMissingAmbiguousAndConflictingFields(t *testing.T) {
	for _, key := range []string{"schema_version", "job_id", "source_id", "source_revision", "status", "exit_code", "duration_seconds", "repository_result"} {
		t.Run("missing_"+key, func(t *testing.T) {
			body := reportObject()
			delete(body, key)
			data, err := json.Marshal(body)
			require.NoError(t, err)
			_, err = executionarchive.ValidateReport(data, expectation())
			require.Error(t, err)
		})
	}
	for _, change := range []func(map[string]any){
		func(b map[string]any) { b["exit_code"] = 1 },
		func(b map[string]any) { b["exit_code"] = -1 },
		func(b map[string]any) { b["exit_code"] = 256 },
		func(b map[string]any) { b["exit_code"] = 0.5 },
		func(b map[string]any) { b["duration_seconds"] = -1 },
		func(b map[string]any) { b["schema_version"] = 2 },
	} {
		body := reportObject()
		change(body)
		data, err := json.Marshal(body)
		require.NoError(t, err)
		_, err = executionarchive.ValidateReport(data, expectation())
		require.Error(t, err)
	}
	data, err := json.Marshal(reportObject())
	require.NoError(t, err)
	for _, invalid := range [][]byte{
		append(append([]byte{}, data...), []byte(" {}")...),
		bytes.Replace(data, []byte(`"exit_code":0`), []byte(`"exit_code":1,"exit_code":0`), 1),
		bytes.Replace(data, []byte(`"exit_code":0`), []byte(`"exit_code":0,"EXIT_CODE":1`), 1),
		bytes.Replace(data, []byte(`"exit_code":0`), []byte(`"EXIT_CODE":1,"exit_code":0`), 1),
		bytes.Replace(data, []byte(`"schema_version":1`), []byte(`"schema_version":null`), 1),
	} {
		_, err = executionarchive.ValidateReport(invalid, expectation())
		require.Error(t, err)
	}
}

func archiveWith(t *testing.T, headers []*tar.Header, payloads [][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for i, header := range headers {
		require.NoError(t, archive.WriteHeader(header))
		if i < len(payloads) {
			_, err := archive.Write(payloads[i])
			require.NoError(t, err)
		}
	}
	// Complete the archive so unsafe entry tests cannot pass only because
	// the required execution evidence is absent.
	report, err := json.Marshal(reportObject())
	require.NoError(t, err)
	for name, data := range map[string][]byte{"report.json": report, "agent-runner.log": []byte("finished\n")} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}))
		_, err := archive.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return output.Bytes()
}

func TestRejectUnsafeArchiveEntries(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "../escaped", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "/absolute", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "a/../report.json", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "C:/file", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "a\\file", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "report.json", Mode: 0600},
		{Name: "hardlink", Typeflag: tar.TypeLink, Linkname: "report.json", Mode: 0600},
		{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0600},
		{Name: "device", Typeflag: tar.TypeChar, Mode: 0600},
		{Name: "script", Typeflag: tar.TypeReg, Mode: 0700},
		{Name: "special", Typeflag: tar.TypeReg, Mode: 04600},
		{Name: "extended", Typeflag: tar.TypeReg, Mode: 0600, PAXRecords: map[string]string{"SCHILY.xattr.user.payload": "unexpected"}},
	} {
		t.Run(header.Name, func(t *testing.T) {
			_, err := validateBytes(t, archiveWith(t, []*tar.Header{header}, nil), expectation())
			require.Error(t, err)
		})
	}
}

func TestRejectTruncationChecksumAndAdditionalStreams(t *testing.T) {
	data := testutil.ExecutionArchive(t, expectation())
	corrupt := append([]byte{}, data...)
	corrupt[len(corrupt)-8] ^= 1
	for _, invalid := range [][]byte{data[:len(data)-1], corrupt, append(append([]byte{}, data...), data...), append(append([]byte{}, data...), 'x')} {
		_, err := validateBytes(t, invalid, expectation())
		require.Error(t, err)
	}
}

func TestRejectDuplicateAndConflictingPaths(t *testing.T) {
	for _, names := range [][]string{{"report.json", "./report.json"}, {"a", "a/report.json"}, {"a/report.json", "a"}} {
		headers := []*tar.Header{}
		for _, name := range names {
			headers = append(headers, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600})
		}
		_, err := validateBytes(t, archiveWith(t, headers, nil), expectation())
		require.Error(t, err)
	}
}

func TestArchiveLimitsAndCancellation(t *testing.T) {
	// Nine individually valid files exceed the total expansion limit.
	var headers []*tar.Header
	var payloads [][]byte
	for i := 0; i < 9; i++ {
		headers = append(headers, &tar.Header{Name: string(rune('a' + i)), Typeflag: tar.TypeReg, Mode: 0600, Size: 8 << 20})
		payloads = append(payloads, make([]byte, 8<<20))
	}
	_, err := validateBytes(t, archiveWith(t, headers, payloads), expectation())
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file := filepath.Join(t.TempDir(), "archive")
	require.NoError(t, os.WriteFile(file, testutil.ExecutionArchive(t, expectation()), 0600))
	_, err = executionarchive.Validate(ctx, file, expectation())
	require.Error(t, err)
}

func TestFailedAndTimedOutReportsDoNotClaimTestResults(t *testing.T) {
	for _, status := range []string{"FAILED", "TIMEOUT"} {
		body := reportObject()
		body["status"] = status
		body["exit_code"] = 1
		if status == "TIMEOUT" {
			body["exit_code"] = 124
		}
		data, err := json.Marshal(body)
		require.NoError(t, err)
		expected := expectation()
		expected.Status = status
		evidence, err := executionarchive.ValidateReport(data, expected)
		require.NoError(t, err)
		require.Equal(t, "guest_reported", evidence.State)
		require.Equal(t, "not_reported", evidence.Tests)
	}
}

func TestCredentialPolicyCoversArtifactNamesAndContents(t *testing.T) {
	for name, data := range map[string][]byte{
		".env.production": []byte("opaque value"),
		".ssh/id_ed25519": []byte("opaque value"),
		"config.json":     []byte(`{"password":"private-password-value"}`),
		"runner.log":      []byte("ghp_" + strings.Repeat("X", 40)),
		"private.txt":     []byte("-----BEGIN PRIVATE KEY-----\nprivate bytes"),
	} {
		t.Run(name, func(t *testing.T) {
			header := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}
			_, err := validateBytes(t, archiveWith(t, []*tar.Header{header}, [][]byte{data}), expectation())
			require.ErrorContains(t, err, "credential")
			require.NotContains(t, err.Error(), string(data))
		})
	}
}

func TestKnownCredentialsUsePrivateFileAndNeverErrors(t *testing.T) {
	secret := []byte("opaque-runtime-credential\nsecond-line")
	header := &tar.Header{Name: "custom.bin", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(secret))}
	data := archiveWith(t, []*tar.Header{header}, [][]byte{secret})
	archive := filepath.Join(t.TempDir(), "archive")
	require.NoError(t, os.WriteFile(archive, data, 0600))
	// This value does not match a recognized credential pattern.
	_, err := executionarchive.Validate(context.Background(), archive, expectation())
	require.NoError(t, err)
	secrets := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(secrets, append(secret, 0), 0600))
	e := expectation()
	args := []string{"--archive", archive, "--job-id", e.JobID, "--source-id", e.SourceID, "--source-revision", e.SourceRevision, "--manifest-sha256", e.ManifestSHA256, "--status", e.Status, "--secret-file", secrets}
	var output bytes.Buffer
	err = executionarchive.RunCommand(context.Background(), args, &output)
	require.ErrorContains(t, err, "known credential")
	require.NotContains(t, err.Error(), string(secret))
	require.Empty(t, output.String())
	require.NoError(t, os.Chmod(secrets, 0644))
	err = executionarchive.RunCommand(context.Background(), args, &output)
	require.ErrorContains(t, err, "private")
}
