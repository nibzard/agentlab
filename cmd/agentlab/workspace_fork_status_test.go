package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceForkStatusUsesBoundAPIAndPreservesRecovery(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"workspace-operation","state":"blocked","target_volume_id":"local-zfs:retained","cleanup_status":"inspection_required"}`))
	}))
	defer server.Close()
	base := commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second}
	for _, args := range [][]string{{"source"}, {"source", "workspace-operation"}} {
		require.NoError(t, runWorkspaceForkStatus(context.Background(), args, base))
	}
	require.Equal(t, []string{"/v1/workspaces/source/forks", "/v1/workspaces/source/forks/workspace-operation"}, paths)
	err := parseAPIError(409, []byte(`{"error":"fork stopped","code":"workspace_fork_inspection_required","retryable":false,"workspace_fork":{"id":"workspace-operation","server_identity":"server-one","source_workspace_id":"source","target_volume_id":"local-zfs:retained","copy_snapshot":"agentlab-copy-proof","state":"blocked","cleanup_status":"inspection_required"}}`))
	var output bytes.Buffer
	writeJSONError(&output, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
	require.Equal(t, "blocked", payload["state"])
	operation := payload["workspace_fork"].(map[string]any)
	require.Equal(t, "local-zfs:retained", operation["target_volume_id"])
	require.Equal(t, "server-one", operation["server_identity"])
}
