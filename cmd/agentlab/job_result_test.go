package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/executionarchive"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
	testutil "github.com/agentlab/agentlab/internal/testing"
	"github.com/stretchr/testify/require"
)

func jobResultServer(t *testing.T, corrupt bool) (*httptest.Server, *int) {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "Initial"}} {
		command := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "code.txt"), []byte("reviewable task result\n"), 0600))
	snapshot, err := reposnapshot.Create(context.Background(), reposnapshot.Options{Repository: root, OutputDir: t.TempDir()})
	require.NoError(t, err)
	metadata, err := os.ReadFile(snapshot.ManifestPath)
	require.NoError(t, err)
	digest := sha256.Sum256(metadata)
	manifestHash := hex.EncodeToString(digest[:])
	prefix := "repository-results/" + manifestHash + "/"
	content := map[string][]byte{prefix + "manifest.json": metadata, prefix + "execution.tar.gz": testutil.ExecutionArchive(t, executionarchive.Expected{JobID: "result-job", SourceID: strings.Repeat("a", 64), SourceRevision: snapshot.Manifest.SourceRevision, ManifestSHA256: manifestHash, Status: "COMPLETED"})}
	for name, path := range map[string]string{"source.bundle": snapshot.BundlePath, "changes.patch": snapshot.PatchPath} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		content[prefix+name] = data
	}
	artifacts := []artifactInfo{}
	for path, data := range content {
		sum := sha256.Sum256(data)
		artifacts = append(artifacts, artifactInfo{Name: filepath.Base(path), Path: path, SizeBytes: int64(len(data)), Sha256: hex.EncodeToString(sum[:])})
	}
	receipt := protocol.RepositoryResult{SourceID: strings.Repeat("a", 64), Execution: &executionarchive.Evidence{State: "guest_reported", AgentExitCode: 0, DurationSeconds: 1, Tests: "not_reported"}, State: "verified", ManifestSHA256: manifestHash, SourceRevision: snapshot.Manifest.SourceRevision, Revision: snapshot.Manifest.Revision, ManifestPath: prefix + "manifest.json", BundlePath: prefix + "source.bundle", PatchPath: prefix + "changes.patch", PatchSHA256: snapshot.Manifest.PatchSHA256, PatchBytes: snapshot.Manifest.PatchBytes, CleanupStatus: "verified", Resources: []protocol.JobResource{{Kind: "sandbox", ID: "1100", State: "removed"}}}
	result, _ := json.Marshal(map[string]any{"artifacts": artifacts, "result": map[string]any{"repository_result": receipt}})
	job := jobResponse{ID: "result-job", RepoURL: protocol.RepositorySnapshotPrefix + strings.Repeat("a", 64), Ref: receipt.SourceRevision, Status: "COMPLETED", Result: result}
	downloads := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "test-server", Features: map[string]bool{"repository_results": true}})
		case "/v1/jobs/result-job":
			json.NewEncoder(w).Encode(job)
		case "/v1/jobs/result-job/artifacts/download":
			*downloads++
			path := r.URL.Query().Get("path")
			data, ok := content[path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if corrupt && strings.HasSuffix(path, "changes.patch") {
				data = []byte("changed artifact")
			}
			w.Write(data)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	return server, downloads
}

func TestJobResultCollectsVerifiedArtifactsAndChecksCache(t *testing.T) {
	server, downloads := jobResultServer(t, false)
	defer server.Close()
	root := t.TempDir()
	var err error
	output := captureStdout(t, func() {
		err = runJobResult(context.Background(), []string{"result-job", "--out", root}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	})
	require.NoError(t, err)
	var result struct {
		Out              string                    `json:"out"`
		PatchPath        string                    `json:"patch_path"`
		RepositoryResult protocol.RepositoryResult `json:"repository_result"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	require.Equal(t, "verified", result.RepositoryResult.State)
	require.Equal(t, 4, *downloads)
	patch, err := os.ReadFile(result.PatchPath)
	require.NoError(t, err)
	require.Contains(t, string(patch), "+reviewable task result")
	info, err := os.Stat(result.PatchPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	captureStdout(t, func() {
		err = runJobResult(context.Background(), []string{"result-job", "--out", root}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	})
	require.NoError(t, err)
	require.Equal(t, 4, *downloads)
	require.NoError(t, os.WriteFile(result.PatchPath, []byte("changed local result"), 0600))
	captureStdout(t, func() {
		err = runJobResult(context.Background(), []string{"result-job", "--out", root}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	})
	require.Error(t, err)
	require.Equal(t, 4, *downloads)
}

func TestJobResultRejectsChangedDownloadWithoutPublishing(t *testing.T) {
	server, _ := jobResultServer(t, true)
	defer server.Close()
	root := t.TempDir()
	err := runJobResult(context.Background(), []string{"result-job", "--out", root}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.Error(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, entry.IsDir(), "failed collection published a directory or leaked staging files")
	}
}

func TestJobResultBlockedOutputIncludesRetainedResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"repository_results": true}})
			return
		}
		result, _ := json.Marshal(map[string]any{"result": map[string]any{"repository_result": protocol.RepositoryResult{State: "blocked", CleanupStatus: "blocked", Action: "inspect the retained sandbox", Resources: []protocol.JobResource{{Kind: "sandbox", ID: "1100", State: "retained"}}}}})
		json.NewEncoder(w).Encode(jobResponse{ID: "result-job", RepoURL: protocol.RepositorySnapshotPrefix + strings.Repeat("a", 64), Status: "FAILED", Result: result})
	}))
	defer server.Close()
	err := runJobResult(context.Background(), []string{"result-job"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	var output strings.Builder
	writeJSONError(&output, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(output.String()), &result))
	require.Equal(t, "blocked", result["state"])
	require.Equal(t, "result-job", result["job_id"])
	require.Len(t, result["resources"], 1)
}

func TestRequestBodyRemainsActiveDuringStreamingRead(t *testing.T) {
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		<-gate
		io.WriteString(w, "second")
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	response, err := client.doRequest(context.Background(), http.MethodGet, "/stream", nil, nil)
	close(gate)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "first second", string(body))
}
