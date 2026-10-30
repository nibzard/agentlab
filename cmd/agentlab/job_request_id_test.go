package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestJobRequestIDIsSentOnlyAfterCapabilityCheck(t *testing.T) {
	sequence := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence = append(sequence, r.URL.Path)
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"idempotent_job_submission": true}})
		case "/v1/jobs":
			require.Equal(t, "stable-key", r.Header.Get("Idempotency-Key"))
			fmt.Fprint(w, `{"id":"job-existing","status":"QUEUED"}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	err := runJobRun(context.Background(), []string{"--request-id", "stable-key", "--repo", "https://example.com/repo.git", "--profile", "default", "--task", "test"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.NoError(t, err)
	require.Equal(t, []string{"/v1/capabilities", "/v1/jobs"}, sequence)
}

func TestJobRequestIDDoesNotCreateUnsupportedWorkspace(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/v1/capabilities", r.URL.Path)
		json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"idempotent_job_submission": true}})
	}))
	defer server.Close()
	err := runJobRun(context.Background(), []string{"--request-id", "stable-key", "--repo", "https://example.com/repo.git", "--profile", "default", "--task", "test", "--workspace-create", "new"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.Error(t, err)
	require.Equal(t, 1, requests)
}

func TestSubmissionRecoveryMetadataKeepsBooleanType(t *testing.T) {
	err := parseAPIError(409, []byte(`{"error":"conflict","code":"idempotency_conflict","message":"key conflict","action":"reuse the original inputs","retryable":false}`))
	var output bytes.Buffer
	writeJSONError(&output, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Equal(t, "idempotency_conflict", payload["code"])
	require.Equal(t, "reuse the original inputs", payload["action"])
	require.Equal(t, false, payload["retryable"])
}

func TestWorkspaceRequestIDPreservesAllocationRecovery(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"idempotent_job_submission": true, "idempotent_workspace_create": true}})
		case "/v1/jobs":
			calls++
			require.Equal(t, "workspace-key", r.Header.Get("Idempotency-Key"))
			var req jobCreateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.NotNil(t, req.WorkspaceCreate)
			require.Equal(t, "new-workspace", req.WorkspaceCreate.Name)
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":"allocation needs inspection","code":"workspace_allocation_inspection_required","action":"preserve the recorded volume","retryable":false,"workspace_allocation":{"job_id":"job-original","workspace_id":"ws-original","name":"new-workspace","storage":"local-zfs","size_gb":80,"state":"allocating","volume_id":"local-zfs:retained","created_at":"2026-10-30T00:00:00Z"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	err := runJobRun(context.Background(), []string{"--request-id", "workspace-key", "--repo", "https://example.com/repo.git", "--profile", "default", "--task", "test", "--workspace-create", "new-workspace", "--workspace-size", "80"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	require.Error(t, err)
	var output bytes.Buffer
	writeJSONError(&output, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Equal(t, "workspace_allocation_inspection_required", payload["code"])
	require.Equal(t, "blocked", payload["state"])
	require.Equal(t, false, payload["retryable"])
	allocation, ok := payload["workspace_allocation"].(map[string]any)
	require.True(t, ok, output.String())
	require.Equal(t, "job-original", allocation["job_id"])
	require.Equal(t, "ws-original", allocation["workspace_id"])
	require.Equal(t, "local-zfs:retained", allocation["volume_id"])
	require.Equal(t, 1, calls)
}
