package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestJobCleanupNegotiatesAndSavesDurableRequest(t *testing.T) {
	for _, feature := range []bool{false, true} {
		posts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/capabilities":
				json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "cleanup-server", Features: map[string]bool{"repository_result_cleanup": feature}})
			case "/v1/jobs/test-job/result/cleanup":
				if r.Method != http.MethodPost {
					t.Errorf("unexpected method %s", r.Method)
				}
				posts++
				json.NewEncoder(w).Encode(map[string]any{"job_id": "test-job", "accepted": true, "repository_result": protocol.RepositoryResult{State: "verified", CleanupStatus: "pending"}, "resources": []protocol.JobResource{}})
			default:
				t.Errorf("unexpected request %s", r.URL.Path)
				w.WriteHeader(404)
			}
		}))
		var err error
		output := captureStdout(t, func() {
			err = runJobCleanup(context.Background(), []string{"test-job"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
		})
		server.Close()
		if !feature {
			require.ErrorContains(t, err, "verified result cleanup")
			require.Zero(t, posts)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, 1, posts)
		var result map[string]any
		require.NoError(t, json.Unmarshal([]byte(output), &result))
		require.Equal(t, "cleanup-server", result["server_identity"])
		require.Equal(t, "agentlab job show test-job", result["next"])
	}
}
