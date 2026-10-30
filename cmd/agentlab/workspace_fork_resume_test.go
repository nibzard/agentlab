package main

import (
	"context"
	"encoding/json"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkspaceForkResumeRequiresAdvertisedRecovery(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported", true: "supported"}[enabled], func(t *testing.T) {
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/capabilities" {
					json.NewEncoder(w).Encode(protocol.Capabilities{ProtocolVersion: 1, APISchemaVersion: 1, ServerIdentity: "server", Features: map[string]bool{"workspace_fork_recovery": enabled}})
					return
				}
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/v1/workspaces/source/forks/original-operation/resume", r.URL.Path)
				mutations++
				w.Write([]byte(`{"id":"original-target"}`))
			}))
			defer server.Close()
			err := runWorkspaceForkResume(context.Background(), []string{"source", "original-operation"}, commonFlags{endpoint: server.URL, jsonOutput: true, timeout: time.Second})
			if enabled {
				require.NoError(t, err)
				require.Equal(t, 1, mutations)
			} else {
				require.Error(t, err)
				require.Zero(t, mutations)
			}
		})
	}
}
