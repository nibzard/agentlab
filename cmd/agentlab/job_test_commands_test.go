package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJobRequestedTestsForwardAndRequireCapability(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "old_server"}[supported], func(t *testing.T) {
			commands := []string{"go test ./...", "printf 'hello\\n'\n"}
			creates := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, 200, map[string]any{"protocol_version": 1, "api_schema_version": 1, "features": map[string]bool{"job_test_commands": supported}})
			})
			mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
				creates++
				var request jobCreateRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, commands, request.TestCommands)
				writeJSON(t, w, 201, jobResponse{ID: "tests-job"})
			})
			socket := startUnixHTTPServer(t, mux)
			captureStdout(t, func() {
				err := runJobRun(context.Background(), []string{"--repo", "https://example.com/repo.git", "--profile", "default", "--task", "change", "--test-command", commands[0], "--test-command", commands[1]}, commonFlags{socketPath: socket, jsonOutput: true, timeout: time.Second})
				if supported {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "upgrade the daemon")
				}
			})
			if supported {
				require.Equal(t, 1, creates)
			} else {
				require.Zero(t, creates)
			}
		})
	}
}

func TestInvalidTestCommandsStopBeforeConnectionAndSessionCreation(t *testing.T) {
	for _, run := range []func(context.Context, []string, commonFlags) error{runJobRun, runJobValidate} {
		err := run(context.Background(), []string{"--repo", "https://example.com/repo.git", "--profile", "default", "--task", "change", "--branch", "feature", "--test-command", ""}, commonFlags{socketPath: "/missing-test-command.sock"})
		require.ErrorContains(t, err, "test_commands")
	}
}
