package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCLIExposureCommands(t *testing.T) {
	var gotCreate exposureCreateRequest
	exposure := exposureResponse{
		Name:      exposureName(9001, 8080),
		VMID:      9001,
		Port:      8080,
		TargetIP:  "10.77.0.10",
		URL:       "tcp://tailnet.example:8080",
		State:     "serving",
		CreatedAt: "2026-02-08T20:30:00Z",
		UpdatedAt: "2026-02-08T20:30:00Z",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/exposures", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&gotCreate); err != nil {
				t.Errorf("decode exposure request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			writeJSON(t, w, http.StatusCreated, exposure)
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, exposuresResponse{Exposures: []exposureResponse{exposure}})
		default:
			t.Errorf("/v1/exposures method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/v1/exposures/"+exposure.Name, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("/v1/exposures/{name} method = %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(t, w, http.StatusOK, exposure)
	})

	socketPath := startUnixHTTPServer(t, mux)
	base := commonFlags{socketPath: socketPath, jsonOutput: false, timeout: time.Second}

	out := captureStdout(t, func() {
		err := runSandboxExpose(context.Background(), []string{"--force", "9001", ":8080"}, base)
		if err != nil {
			t.Fatalf("runSandboxExpose() error = %v", err)
		}
	})
	if gotCreate.VMID != 9001 || gotCreate.Port != 8080 {
		t.Fatalf("unexpected exposure create payload: %#v", gotCreate)
	}
	if gotCreate.Name != exposure.Name {
		t.Fatalf("exposure name = %q, want %q", gotCreate.Name, exposure.Name)
	}
	if !gotCreate.Force {
		t.Fatalf("expected exposure create to set force")
	}
	if !strings.Contains(out, exposure.Name) || !strings.Contains(out, exposure.URL) {
		t.Fatalf("expected exposure output, got %q", out)
	}

	out = captureStdout(t, func() {
		err := runSandboxExposed(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runSandboxExposed() error = %v", err)
		}
	})
	if !strings.Contains(out, exposure.Name) || !strings.Contains(out, exposure.State) {
		t.Fatalf("expected exposure list output, got %q", out)
	}

	out = captureStdout(t, func() {
		err := runSandboxUnexpose(context.Background(), []string{exposure.Name}, base)
		if err != nil {
			t.Fatalf("runSandboxUnexpose() error = %v", err)
		}
	})
	if !strings.Contains(out, exposure.Name) {
		t.Fatalf("expected unexpose output, got %q", out)
	}

	base.jsonOutput = true
	jsonOut := captureStdout(t, func() {
		err := runSandboxExposed(context.Background(), nil, base)
		if err != nil {
			t.Fatalf("runSandboxExposed(json) error = %v", err)
		}
	})
	var resp exposuresResponse
	if err := json.Unmarshal([]byte(jsonOut), &resp); err != nil {
		t.Fatalf("unmarshal exposures json: %v", err)
	}
	if len(resp.Exposures) != 1 || resp.Exposures[0].Name != exposure.Name {
		t.Fatalf("unexpected exposures response: %#v", resp.Exposures)
	}
}
