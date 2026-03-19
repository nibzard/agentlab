package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunSandboxInventory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/sandboxes/inventory" {
			t.Fatalf("path = %s, want /v1/sandboxes/inventory", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxes":[{"vmid":1053,"name":"openclaw","managed":true,"profile":"yolo","agentlab_state":"DESTROYED","proxmox_status":"running","agentlab_ip":"10.77.0.195","tailscale_dns":"openclaw.tailnet.ts.net","tailscale_ips":["100.64.0.10"],"drift":["restored_after_destroy"]}]}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		err := runSandboxInventory(context.Background(), nil, commonFlags{
			endpoint: srv.URL,
			timeout:  time.Second,
		})
		if err != nil {
			t.Fatalf("runSandboxInventory() error = %v", err)
		}
	})

	if !strings.Contains(out, "openclaw") {
		t.Fatalf("stdout missing sandbox name: %q", out)
	}
	if !strings.Contains(out, "100.64.0.10") {
		t.Fatalf("stdout missing tailscale ip: %q", out)
	}
	if !strings.Contains(out, "restored_after_destroy") {
		t.Fatalf("stdout missing drift code: %q", out)
	}
}

func TestRunSandboxReconcileApply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/sandboxes/reconcile" {
			t.Fatalf("path = %s, want /v1/sandboxes/reconcile", r.URL.Path)
		}
		var req sandboxReconcileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !req.Apply {
			t.Fatalf("expected apply=true")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dry_run":false,"checked":1,"drifted":0,"reconciled":1,"results":[{"vmid":1053,"name":"openclaw","managed":true,"profile":"yolo","agentlab_state":"RUNNING","proxmox_status":"running","agentlab_ip":"10.77.0.195"}]}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		err := runSandboxReconcile(context.Background(), []string{"--apply"}, commonFlags{
			endpoint: srv.URL,
			timeout:  time.Second,
		})
		if err != nil {
			t.Fatalf("runSandboxReconcile() error = %v", err)
		}
	})

	if !strings.Contains(out, "Reconcile: applied") {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(out, "Reconciled: 1") {
		t.Fatalf("stdout = %q", out)
	}
}
