package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startUnixHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "agentlab.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	return socketPath
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func TestCLIHappyPathJobAndSandbox(t *testing.T) {
	var mu sync.Mutex
	var gotJobReq jobCreateRequest
	var gotJobReqCount int

	createdAt := "2026-01-30T12:00:00Z"
	updatedAt := "2026-01-30T12:01:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/jobs method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req jobCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode job request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotJobReq = req
		gotJobReqCount++
		mu.Unlock()

		resp := jobResponse{
			ID:         "job-123",
			RepoURL:    req.RepoURL,
			Ref:        req.Ref,
			Profile:    req.Profile,
			Task:       req.Task,
			Mode:       req.Mode,
			TTLMinutes: req.TTLMinutes,
			Keepalive:  req.Keepalive != nil && *req.Keepalive,
			Status:     "QUEUED",
			CreatedAt:  createdAt,
			UpdatedAt:  createdAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/jobs/job-123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/jobs/job-123 method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("events_tail") != "2" {
			t.Errorf("events_tail = %q", r.URL.Query().Get("events_tail"))
		}
		resp := jobResponse{
			ID:         "job-123",
			RepoURL:    "https://github.com/example/repo",
			Ref:        "main",
			Profile:    "yolo",
			Task:       "run tests",
			Mode:       "dangerous",
			TTLMinutes: intPtr(2),
			Keepalive:  true,
			Status:     "RUNNING",
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
			Events: []eventResponse{
				{ID: 1, Timestamp: "2026-01-30T12:00:30Z", Kind: "job.started", JobID: "job-123", Message: "started"},
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/sandboxes method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		lease := "2026-01-30T13:00:00Z"
		resp := sandboxesResponse{Sandboxes: []sandboxResponse{
			{
				VMID:          9001,
				Name:          "sandbox-9001",
				Profile:       "yolo",
				State:         "RUNNING",
				IP:            "10.77.0.10",
				Keepalive:     true,
				LeaseExpires:  &lease,
				CreatedAt:     createdAt,
				LastUpdatedAt: updatedAt,
			},
		}}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runJobRun(context.Background(), []string{
			"--repo", "https://github.com/example/repo",
			"--ref", "main",
			"--profile", "yolo",
			"--task", "run tests",
			"--mode", "dangerous",
			"--ttl", "90s",
			"--keepalive",
		}, base)
		if err != nil {
			t.Fatalf("runJobRun() error = %v", err)
		}
	})
	if !strings.Contains(out, "ID: job-123") {
		t.Fatalf("expected job output, got %q", out)
	}

	mu.Lock()
	gotReq := gotJobReq
	count := gotJobReqCount
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 job request, got %d", count)
	}
	if gotReq.RepoURL != "https://github.com/example/repo" {
		t.Fatalf("repo_url = %q", gotReq.RepoURL)
	}
	if gotReq.Profile != "yolo" {
		t.Fatalf("profile = %q", gotReq.Profile)
	}
	if gotReq.Task != "run tests" {
		t.Fatalf("task = %q", gotReq.Task)
	}
	if gotReq.TTLMinutes == nil || *gotReq.TTLMinutes != 2 {
		t.Fatalf("ttl_minutes = %v", gotReq.TTLMinutes)
	}
	if gotReq.Keepalive == nil || !*gotReq.Keepalive {
		t.Fatalf("keepalive not set")
	}

	out = captureStdout(t, func() {
		err := runJobShow(context.Background(), []string{"--events-tail", "2", "job-123"}, base)
		if err != nil {
			t.Fatalf("runJobShow() error = %v", err)
		}
	})
	if !strings.Contains(out, "Events:") {
		t.Fatalf("expected events output, got %q", out)
	}
	if !strings.Contains(out, "job=job-123") {
		t.Fatalf("expected job id in events, got %q", out)
	}

	out = captureStdout(t, func() {
		err := runSandboxList(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runSandboxList() error = %v", err)
		}
	})
	if !strings.Contains(out, "sandbox-9001") || !strings.Contains(out, "RUNNING") {
		t.Fatalf("expected sandbox list output, got %q", out)
	}
}

func TestCLISandboxStartStopHappyPath(t *testing.T) {
	createdAt := "2026-01-30T12:00:00Z"
	updatedAt := "2026-01-30T12:01:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9001/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes/9001/start method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "RUNNING",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes/9001/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes/9001/stop method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "STOPPED",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runSandboxStart(context.Background(), []string{"9001"}, base)
		if err != nil {
			t.Fatalf("runSandboxStart() error = %v", err)
		}
	})
	if !strings.Contains(out, "started") || !strings.Contains(out, "RUNNING") {
		t.Fatalf("expected start output, got %q", out)
	}

	out = captureStdout(t, func() {
		err := runSandboxStop(context.Background(), []string{"9001"}, base)
		if err != nil {
			t.Fatalf("runSandboxStop() error = %v", err)
		}
	})
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "STOPPED") {
		t.Fatalf("expected stop output, got %q", out)
	}
}

func TestCLISandboxStopAllHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	var gotStopAll sandboxStopAllRequest
	mux.HandleFunc("/v1/sandboxes/stop_all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes/stop_all method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotStopAll); err != nil {
			t.Errorf("decode stop_all request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := sandboxStopAllResponse{
			Total:   3,
			Stopped: 1,
			Skipped: 1,
			Failed:  1,
			Results: []sandboxStopAllResult{
				{VMID: 9001, State: "STOPPED", Result: "stopped"},
				{VMID: 9002, State: "STOPPED", Result: "skipped"},
				{VMID: 9003, State: "RUNNING", Result: "failed", Error: "backend timeout"},
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runSandboxStop(context.Background(), []string{"--all", "--force"}, base)
		if err != nil {
			t.Fatalf("runSandboxStop(--all) error = %v", err)
		}
	})
	if !gotStopAll.Force {
		t.Fatalf("expected stop_all to set force")
	}
	if !strings.Contains(out, "stop-all complete") {
		t.Fatalf("expected stop-all summary, got %q", out)
	}
	if !strings.Contains(out, "stopped=1") || !strings.Contains(out, "skipped=1") || !strings.Contains(out, "failed=1") {
		t.Fatalf("expected stop-all counts, got %q", out)
	}
	if !strings.Contains(out, "skipped: vmid=9002") {
		t.Fatalf("expected skipped entry, got %q", out)
	}
	if !strings.Contains(out, "failed: vmid=9003") {
		t.Fatalf("expected failed entry, got %q", out)
	}
}

func TestCLIProfileListHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/profiles method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := profilesResponse{Profiles: []profileResponse{
			{Name: "alpha", TemplateVMID: 9000, UpdatedAt: "2026-02-08T12:00:00Z"},
			{Name: "beta", TemplateVMID: 9100, UpdatedAt: "2026-02-08T12:05:00Z"},
		}}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runProfileList(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runProfileList() error = %v", err)
		}
	})
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "alpha") {
		t.Fatalf("expected profile list output, got %q", out)
	}
	if !strings.Contains(out, "9000") {
		t.Fatalf("expected template id in output, got %q", out)
	}
}

func TestCLIStatusHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/status method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := statusResponse{
			Sandboxes:    map[string]int{"RUNNING": 2, "STOPPED": 1},
			Jobs:         map[string]int{"QUEUED": 1, "FAILED": 2},
			NetworkModes: map[string]int{"nat": 2, "off": 0, "allowlist": 1},
			Artifacts: statusArtifactsResponse{
				Root:       "/var/lib/agentlab/artifacts",
				TotalBytes: 1000,
				FreeBytes:  250,
				UsedBytes:  750,
			},
			Metrics: statusMetricsResponse{Enabled: true},
			RecentFailures: []eventResponse{
				{
					ID:          1,
					Timestamp:   "2026-02-08T12:00:00Z",
					Kind:        "job.failed",
					SandboxVMID: intPtr(9001),
					JobID:       "job-1",
					Message:     "boom",
				},
			},
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runStatusCommand(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runStatusCommand() error = %v", err)
		}
	})
	if !strings.Contains(out, "Sandboxes:") || !strings.Contains(out, "RUNNING") {
		t.Fatalf("expected sandboxes in output, got %q", out)
	}
	if !strings.Contains(out, "Network Modes:") || !strings.Contains(out, "nat") {
		t.Fatalf("expected network modes in output, got %q", out)
	}
	if !strings.Contains(out, "Artifacts:") || !strings.Contains(out, "Metrics:") {
		t.Fatalf("expected artifacts/metrics output, got %q", out)
	}
	if !strings.Contains(out, "job.failed") {
		t.Fatalf("expected recent failure output, got %q", out)
	}

	base.jsonOutput = true
	jsonOut := captureStdout(t, func() {
		err := runStatusCommand(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runStatusCommand(json) error = %v", err)
		}
	})
	var got statusResponse
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("unmarshal status json: %v", err)
	}
	if got.Metrics.Enabled != true {
		t.Fatalf("expected metrics enabled, got %#v", got.Metrics.Enabled)
	}
}
