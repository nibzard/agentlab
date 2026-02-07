package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSHWaitsForSandboxIP(t *testing.T) {
	var calls int32

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/1001", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		n := atomic.AddInt32(&calls, 1)
		ip := ""
		if n >= 2 {
			ip = "203.0.113.10"
		}
		resp := sandboxResponse{
			VMID:          1001,
			Name:          "sandbox-1001",
			Profile:       "ubuntu-24-04-ai",
			State:         "RUNNING",
			IP:            ip,
			CreatedAt:     "2026-02-01T00:00:00Z",
			LastUpdatedAt: "2026-02-01T00:00:00Z",
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, timeout: 2 * time.Second}

	out := captureStdout(t, func() {
		if err := runSSHCommand(context.Background(), []string{"1001"}, base); err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("calls = %d, want >= 2", got)
	}
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "ssh ") {
		t.Fatalf("output = %q, want ssh command", out)
	}
	if !strings.Contains(out, "agent@203.0.113.10") {
		t.Fatalf("output = %q, want agent@203.0.113.10", out)
	}
}

func TestSSHWaitTimeoutWithoutIP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/42", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxResponse{VMID: 42, Name: "sandbox-42", Profile: "p", State: "RUNNING", CreatedAt: "x", LastUpdatedAt: "x"}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, timeout: 150 * time.Millisecond}

	err := runSSHCommand(context.Background(), []string{"42"}, base)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(err.Error(), "no IP yet") && !strings.Contains(err.Error(), "no ip yet") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Ensure the error isn't the generic request timeout; this should be the "no IP yet" sentinel.
	if strings.Contains(err.Error(), strconv.Itoa(int(http.StatusRequestTimeout))) {
		t.Fatalf("unexpected HTTP timeout error: %v", err)
	}
}
