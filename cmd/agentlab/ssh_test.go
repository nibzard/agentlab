package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSSHStartsStoppedSandbox(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"
	var startCalled bool

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9001", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/sandboxes/9001 method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "STOPPED",
			IP:            "",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes/9001/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes/9001/start method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		startCalled = true
		resp := sandboxResponse{
			VMID:          9001,
			Name:          "sandbox-9001",
			Profile:       "yolo",
			State:         "RUNNING",
			IP:            "203.0.113.5",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runSSHCommand(context.Background(), []string{"9001"}, base)
		if err != nil {
			t.Fatalf("runSSHCommand() error = %v", err)
		}
	})

	if !startCalled {
		t.Fatalf("expected start endpoint to be called")
	}
	if !strings.Contains(out, "ssh") || !strings.Contains(out, "203.0.113.5") {
		t.Fatalf("expected ssh command output, got %q", out)
	}
}

func TestSSHNoStartStoppedSandbox(t *testing.T) {
	createdAt := "2026-02-08T12:00:00Z"
	updatedAt := "2026-02-08T12:02:00Z"
	var startCalled bool

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes/9002", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/sandboxes/9002 method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := sandboxResponse{
			VMID:          9002,
			Name:          "sandbox-9002",
			Profile:       "yolo",
			State:         "STOPPED",
			IP:            "203.0.113.6",
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes/9002/start", func(w http.ResponseWriter, r *http.Request) {
		startCalled = true
		w.WriteHeader(http.StatusOK)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runSSHCommand(context.Background(), []string{"--no-start", "9002"}, base)
	if err == nil {
		t.Fatalf("expected error for stopped sandbox with --no-start")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("expected stopped error, got %v", err)
	}
	if startCalled {
		t.Fatalf("did not expect start endpoint to be called")
	}
}
