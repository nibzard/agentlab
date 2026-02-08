package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSandboxNewModifiersResolveProfile(t *testing.T) {
	createdAt := "2026-02-08T20:00:00Z"
	updatedAt := "2026-02-08T20:00:10Z"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("/v1/profiles method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		resp := profilesResponse{Profiles: []profileResponse{
			{Name: "secure-small", TemplateVMID: 9000, UpdatedAt: updatedAt},
			{Name: "yolo-ephemeral", TemplateVMID: 9000, UpdatedAt: updatedAt},
		}}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("/v1/sandboxes method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req sandboxCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode sandbox request: %v", err)
		}
		if req.Profile != "secure-small" {
			t.Fatalf("profile = %q", req.Profile)
		}
		resp := sandboxResponse{
			VMID:          9010,
			Name:          "mod-sandbox",
			Profile:       req.Profile,
			State:         "RUNNING",
			IP:            "10.77.0.10",
			Keepalive:     false,
			CreatedAt:     createdAt,
			LastUpdatedAt: updatedAt,
		}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	_ = captureStdout(t, func() {
		err := runSandboxNew(context.Background(), []string{
			"--name", "mod-sandbox",
			"--ttl", "30m",
			"+small",
			"+secure",
		}, base)
		if err != nil {
			t.Fatalf("runSandboxNew() error = %v", err)
		}
	})
}

func TestSandboxNewUnknownModifier(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		resp := profilesResponse{Profiles: []profileResponse{
			{Name: "secure-small", TemplateVMID: 9000, UpdatedAt: "2026-02-08T20:00:10Z"},
		}}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("/v1/sandboxes should not be called")
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runSandboxNew(context.Background(), []string{"+tiny"}, base)
	if err == nil {
		t.Fatalf("expected error for unknown modifier")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown modifier") {
		t.Fatalf("expected unknown modifier error, got %q", msg)
	}
	if !strings.Contains(msg, "Valid modifiers") {
		t.Fatalf("expected valid modifiers list, got %q", msg)
	}
}

func TestSandboxNewUnknownProfileSuggests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		resp := profilesResponse{Profiles: []profileResponse{
			{Name: "secure-small", TemplateVMID: 9000, UpdatedAt: "2026-02-08T20:00:10Z"},
			{Name: "yolo-ephemeral", TemplateVMID: 9000, UpdatedAt: "2026-02-08T20:00:10Z"},
		}}
		writeJSON(t, w, http.StatusOK, resp)
	})
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("/v1/sandboxes should not be called")
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	err := runSandboxNew(context.Background(), []string{"--profile", "secure-smal"}, base)
	if err == nil {
		t.Fatalf("expected error for unknown profile")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "did you mean") {
		t.Fatalf("expected suggestion in error, got %q", msg)
	}
	if !strings.Contains(msg, "secure-small") {
		t.Fatalf("expected suggested profile, got %q", msg)
	}
}

func TestSandboxNewProfileAndModifiersConflict(t *testing.T) {
	err := runSandboxNew(context.Background(), []string{"--profile", "yolo-ephemeral", "+small"}, commonFlags{})
	if err == nil {
		t.Fatalf("expected error for profile/modifier conflict")
	}
	if !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("expected conflict error, got %q", err.Error())
	}
}
