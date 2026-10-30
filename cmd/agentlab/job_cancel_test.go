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

func TestJobCancelWaitTimeoutDoesNotClaimServerFailure(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, Features: map[string]bool{"job_cancellation": true}})
			return
		}
		require.Equal(t, "/v1/jobs/job-1/cancel", r.URL.Path)
		if r.Method == "POST" {
			posts++
		} else {
			require.Equal(t, "GET", r.Method)
		}
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(protocol.JobCancellation{JobID: "job-1", State: "requested", WorkStopped: false, CleanupStatus: "pending", Resources: []protocol.JobResource{}})
	}))
	defer server.Close()
	var err error
	output := captureStdout(t, func() {
		err = runJobCancel(context.Background(), []string{"--wait", "1ms", "job-1"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
	})
	require.NoError(t, err)
	require.Equal(t, 1, posts)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	require.Equal(t, true, result["wait_timed_out"])
	require.Equal(t, "requested", result["state"])
	require.Equal(t, false, result["work_stopped"])
}
