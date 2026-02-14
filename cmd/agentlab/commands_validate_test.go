package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunJobValidateCommandRequestShapeAndPlanOutput(t *testing.T) {
	var gotReq jobValidatePlanRequest

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		resp := jobValidatePlanResponse{
			OK: true,
			Warnings: []preflightIssue{
				{Code: "W001", Field: "workspace", Message: "workspace will be warmed"},
			},
			Plan: &jobValidatePlan{
				RepoURL:   "https://github.com/org/repo",
				Ref:       "main",
				Profile:   "yolo",
				Task:      "run tests",
				Mode:      "dangerous",
				TTLMinutes: func(v int) *int { return &v }(2),
				Keepalive: true,
				WorkspaceCreate: &workspaceCreateRequest{
					Name:    "ws-data",
					SizeGB:  80,
					Storage: "local-zfs",
				},
				WorkspaceWaitSeconds: func(v int) *int { return &v }(120),
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runJobValidate(context.Background(), []string{
			"--repo", "https://github.com/org/repo",
			"--profile", "yolo",
			"--task", "run tests",
			"--ref", "main",
			"--mode", "dangerous",
			"--ttl", "90s",
			"--keepalive",
			"--workspace", "new:ws-data",
			"--workspace-size", "80",
			"--workspace-storage", "local-zfs",
			"--workspace-wait", "2m",
		}, base)
		require.NoError(t, err)
	})

	assert.Equal(t, "https://github.com/org/repo", gotReq.RepoURL)
	assert.Equal(t, "main", gotReq.Ref)
	assert.Equal(t, "yolo", gotReq.Profile)
	assert.Equal(t, "run tests", gotReq.Task)
	assert.Equal(t, "dangerous", gotReq.Mode)
	require.NotNil(t, gotReq.TTLMinutes)
	assert.Equal(t, 2, *gotReq.TTLMinutes)
	require.NotNil(t, gotReq.Keepalive)
	assert.True(t, *gotReq.Keepalive)
	require.NotNil(t, gotReq.WorkspaceCreate)
	assert.Equal(t, "ws-data", gotReq.WorkspaceCreate.Name)
	assert.Equal(t, 80, gotReq.WorkspaceCreate.SizeGB)
	assert.Equal(t, "local-zfs", gotReq.WorkspaceCreate.Storage)
	require.NotNil(t, gotReq.WorkspaceWaitSeconds)
	assert.Equal(t, 120, *gotReq.WorkspaceWaitSeconds)
	assert.Nil(t, gotReq.WorkspaceID)
	assert.Nil(t, gotReq.SessionID)

	assert.Contains(t, output, "Plan:")
	assert.Contains(t, output, "Repo: https://github.com/org/repo")
	assert.Contains(t, output, "Warnings:")
	assert.Contains(t, output, "Validation passed")
}

func TestRunJobValidateCommandFailureReturnsHintedCLIError(t *testing.T) {
	var gotReq jobValidatePlanRequest

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := jobValidatePlanResponse{
			OK: false,
			Errors: []preflightIssue{
				{Code: "E001", Field: "workspace", Message: "invalid workspace id"},
			},
			Plan: &jobValidatePlan{
				RepoURL: "https://github.com/org/repo",
				Profile: "yolo",
				Task:    "run tests",
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runJobValidate(context.Background(), []string{
			"--repo", "https://github.com/org/repo",
			"--profile", "yolo",
			"--task", "run tests",
		}, base)
		require.Error(t, err)
		var cliErr *cliError
		if assert.ErrorAs(t, err, &cliErr) {
			assert.Equal(t, "job preflight validation failed", cliErr.Error())
			assert.Equal(t, "agentlab job run --help", cliErr.next)
			assert.Equal(t, []string{"fix listed validation errors", "rerun with corrected flags"}, cliErr.hints)
		}
	})

	assert.Contains(t, output, "Errors:")
	assert.Contains(t, output, "invalid workspace id [E001] (workspace)")
	assert.Contains(t, output, "Validation failed")
}

func TestRunJobValidateCommandJSONOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := jobValidatePlanResponse{
			OK: true,
			Plan: &jobValidatePlan{
				RepoURL: "https://github.com/org/repo",
				Profile: "yolo",
				Task:    "run tests",
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: true, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runJobValidate(context.Background(), []string{
			"--repo", "https://github.com/org/repo",
			"--profile", "yolo",
			"--task", "run tests",
		}, base)
		require.NoError(t, err)
	})

	var resp jobValidatePlanResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assert.True(t, resp.OK)
	assert.NotNil(t, resp.Plan)
	assert.Equal(t, "yolo", resp.Plan.Profile)
	assert.Equal(t, "run tests", resp.Plan.Task)
}

func TestRunJobValidateCommandJSONFailureReturnsPrintedJSONOnlyError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := jobValidatePlanResponse{
			OK: false,
			Errors: []preflightIssue{
				{Code: "E001", Field: "task", Message: "invalid task"},
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: true, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runJobValidate(context.Background(), []string{
			"--repo", "https://github.com/org/repo",
			"--profile", "yolo",
			"--task", "run tests",
		}, base)
		require.Error(t, err)
		var printedErr *printedJSONOnlyError
		if assert.ErrorAs(t, err, &printedErr) {
			assert.EqualError(t, printedErr, "job preflight validation failed")
		}
	})

	var parsed jobValidatePlanResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assert.False(t, parsed.OK)
	assert.Len(t, parsed.Errors, 1)
}

func TestRunSandboxValidateCommandRequestShapeAndPlanOutput(t *testing.T) {
	var gotReq sandboxValidatePlanRequest

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp := sandboxValidatePlanResponse{
			OK: true,
			Warnings: []preflightIssue{
				{Code: "W010", Field: "ttl", Message: "ttl will be rounded"},
			},
			Plan: &sandboxValidatePlan{
				Name:       "sbx01",
				Profile:    "yolo",
				Keepalive:  true,
				TTLMinutes: func(v int) *int { return &v }(30),
				Workspace:  func(v string) *string { return &v }("ws-1"),
				VMID:       func(v int) *int { return &v }(9001),
				JobID:      "job-1",
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runSandboxValidate(context.Background(), []string{
			"--name", "sbx01",
			"--profile", "yolo",
			"--ttl", "30m",
			"--keepalive",
			"--workspace", "ws-1",
			"--vmid", "9001",
			"--job", "job-1",
		}, base)
		require.NoError(t, err)
	})

	assert.Equal(t, "sbx01", gotReq.Name)
	assert.Equal(t, "yolo", gotReq.Profile)
	require.NotNil(t, gotReq.TTLMinutes)
	assert.Equal(t, 30, *gotReq.TTLMinutes)
	require.NotNil(t, gotReq.Keepalive)
	assert.True(t, *gotReq.Keepalive)
	require.NotNil(t, gotReq.Workspace)
	assert.Equal(t, "ws-1", *gotReq.Workspace)
	require.NotNil(t, gotReq.VMID)
	assert.Equal(t, 9001, *gotReq.VMID)
	assert.Equal(t, "job-1", gotReq.JobID)

	assert.Contains(t, output, "Plan:")
	assert.Contains(t, output, "Profile: yolo")
	assert.Contains(t, output, "Validation passed")
}

func TestRunSandboxValidateCommandFailureAndJSONOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/validate-plan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxValidatePlanResponse{
			OK: false,
			Errors: []preflightIssue{
				{Code: "E010", Field: "vmid", Message: "vmid already in use"},
			},
			Plan: &sandboxValidatePlan{Profile: "yolo"},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	output := captureStdout(t, func() {
		err := runSandboxValidate(context.Background(), []string{
			"--name", "sbx01",
			"--profile", "yolo",
		}, base)
		require.Error(t, err)
		var cliErr *cliError
		if assert.ErrorAs(t, err, &cliErr) {
			assert.Equal(t, "sandbox preflight validation failed", cliErr.Error())
			assert.Equal(t, "agentlab sandbox new --help", cliErr.next)
			assert.Equal(t, []string{"fix listed validation errors", "rerun with corrected flags"}, cliErr.hints)
		}
	})

	assert.Contains(t, output, "Errors:")
	assert.Contains(t, output, "vmid already in use [E010] (vmid)")
	assert.Contains(t, output, "Validation failed")

	output = captureStdout(t, func() {
		base := commonFlags{socketPath: socketPath, jsonOutput: true, timeout: time.Second}
		err := runSandboxValidate(context.Background(), []string{
			"--name", "sbx01",
			"--profile", "yolo",
		}, base)
		require.Error(t, err)
		var printedErr *printedJSONOnlyError
		if assert.ErrorAs(t, err, &printedErr) {
			assert.EqualError(t, printedErr, "sandbox preflight validation failed")
		}
	})

	var parsed sandboxValidatePlanResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assert.False(t, parsed.OK)
	assert.Len(t, parsed.Errors, 1)
}
