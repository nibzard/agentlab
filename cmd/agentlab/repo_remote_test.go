package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/reposnapshot"
	"github.com/stretchr/testify/require"
)

func TestRepositoryUploadStreamsMatchingParts(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	content := []byte("fixture bundle bytes")
	require.NoError(t, os.WriteFile(bundle, content, 0600))
	manifest := reposnapshot.Manifest{PolicyVersion: 1, Revision: strings.Repeat("a", 40), BundleSHA256: strings.Repeat("b", 64), BundleBytes: int64(len(content))}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer request-credential", r.Header.Get("Authorization"))
		require.Equal(t, "/v1/repositories", r.URL.Path)
		reader, err := r.MultipartReader()
		require.NoError(t, err)
		part, err := reader.NextPart()
		require.NoError(t, err)
		require.Equal(t, "manifest", part.FormName())
		actual, err := reposnapshot.ReadManifest(part)
		require.NoError(t, err)
		require.Equal(t, manifest, actual)
		part, err = reader.NextPart()
		require.NoError(t, err)
		require.Equal(t, "bundle", part.FormName())
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		require.True(t, bytes.Equal(content, data))
		_, err = reader.NextPart()
		require.ErrorIs(t, err, io.EOF)
		json.NewEncoder(w).Encode(protocol.RepositorySnapshot{ID: strings.Repeat("c", 64), Manifest: manifest})
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL, Token: "request-credential"}, time.Second)
	result, err := uploadRepositorySnapshot(context.Background(), client, bundle, manifest)
	require.NoError(t, err)
	require.Equal(t, manifest.Revision, result.Manifest.Revision)
}

func TestRepositoryUploadEarlyRejectionStopsWriter(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	require.NoError(t, os.WriteFile(bundle, bytes.Repeat([]byte("x"), 1<<20), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"forbidden","message":"request denied"}`)
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	_, err := uploadRepositorySnapshot(context.Background(), client, bundle, reposnapshot.Manifest{})
	var response apiResponseError
	require.True(t, errors.As(err, &response))
	require.Equal(t, 403, response.Status)
}

func TestRepoUploadChecksCapabilitiesBeforeReadingSource(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/v1/capabilities", r.URL.Path)
		json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"repository_upload": false}})
	}))
	defer server.Close()
	err := runRepoUpload(context.Background(), []string{"--manifest", filepath.Join(t.TempDir(), "missing.json")}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	var blocked *blockedRequirement
	require.True(t, errors.As(err, &blocked))
	require.Equal(t, "repository_upload", blocked.Requirement)
	require.Equal(t, 1, requests)
}

func TestRemoteRepoCommandsRequireSelectedContext(t *testing.T) {
	useTempClientConfig(t)
	t.Setenv("AGENTLAB_CONTEXT", "unavailable")
	_, _, err := parseGlobal([]string{"repo", "upload", "--manifest", "unused.json"})
	require.Error(t, err)
}

func TestSnapshotJobChecksCapabilityBeforeSubmission(t *testing.T) {
	for _, command := range []string{"run", "validate"} {
		t.Run(command, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				require.Equal(t, "/v1/capabilities", r.URL.Path)
				json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"idempotent_job_submission": true, "repository_snapshot_jobs": false}})
			}))
			defer server.Close()
			args := []string{"--repo", protocol.RepositorySnapshotPrefix + strings.Repeat("a", 64), "--ref", strings.Repeat("b", 40), "--profile", "default", "--task", "test local source"}
			base := commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second}
			var err error
			if command == "run" {
				err = runJobRun(context.Background(), args, base)
			} else {
				err = runJobValidate(context.Background(), args, base)
			}
			var blocked *blockedRequirement
			require.True(t, errors.As(err, &blocked))
			require.Equal(t, "repository_snapshot_jobs", blocked.Requirement)
			require.Equal(t, 1, requests)
		})
	}
}

func TestSnapshotJobRejectsDependentResourcesBeforeMutation(t *testing.T) {
	args := []string{"--repo", protocol.RepositorySnapshotPrefix + strings.Repeat("a", 64), "--profile", "default", "--task", "test", "--branch", "feature"}
	for _, command := range []string{"run", "validate"} {
		var err error
		if command == "run" {
			err = runJobRun(context.Background(), args, commonFlags{jsonOutput: true})
		} else {
			err = runJobValidate(context.Background(), args, commonFlags{jsonOutput: true})
		}
		var blocked *blockedRequirement
		require.True(t, errors.As(err, &blocked))
		require.Equal(t, "stateless_vm_job", blocked.Requirement)
	}
}

func TestRepositoryUploadRedirectCannotPublishReceipt(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	content := []byte("fixture bundle")
	require.NoError(t, os.WriteFile(bundle, content, 0600))
	manifest := reposnapshot.Manifest{PolicyVersion: 1, Revision: strings.Repeat("a", 40), BundleSHA256: strings.Repeat("b", 64), BundleBytes: int64(len(content))}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		w.Header().Set("Location", "https://must-not-contact.invalid")
		w.WriteHeader(http.StatusTemporaryRedirect)
		json.NewEncoder(w).Encode(protocol.RepositorySnapshot{ID: strings.Repeat("c", 64), Manifest: manifest})
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	_, err := uploadRepositorySnapshot(context.Background(), client, bundle, manifest)
	var blocked *blockedRequirement
	require.ErrorAs(t, err, &blocked)
	require.Equal(t, "endpoint_redirect", blocked.Code)
}
