package testing

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"testing"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/stretchr/testify/require"
)

// ExecutionArchive creates complete guest evidence for integration tests.
func ExecutionArchive(t *testing.T, expected executionarchive.Expected) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema_version": 1, "job_id": expected.JobID, "source_id": expected.SourceID,
		"source_revision": expected.SourceRevision, "status": expected.Status,
		"exit_code": 0, "duration_seconds": 1,
		"repository_result": map[string]string{"manifest_sha256": expected.ManifestSHA256},
	})
	require.NoError(t, err)
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, content := range map[string][]byte{"report.json": data, "agent-runner.log": []byte("agent finished\n")} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(content))}))
		_, err := archive.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return output.Bytes()
}
