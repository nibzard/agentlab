package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunWorkspaceLeaseClear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/workspaces/ws-1/lease/clear" {
			t.Fatalf("path = %s, want /v1/workspaces/ws-1/lease/clear", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace":{"id":"ws-1","name":"dev","storage":"local-zfs","volid":"local-zfs:vm-10-disk-1","size_gb":10,"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},"cleared":true,"previous_owner":"session:branch-main"}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		err := runWorkspaceLeaseClear(context.Background(), []string{"ws-1"}, commonFlags{
			endpoint:   srv.URL,
			timeout:    time.Second,
			jsonOutput: false,
		})
		if err != nil {
			t.Fatalf("runWorkspaceLeaseClear() error = %v", err)
		}
	})

	if out != "workspace dev lease cleared (previous owner=session:branch-main)\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRunWorkspaceLeaseClearAlreadyClear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workspace":{"id":"ws-2","name":"ops","storage":"local-zfs","volid":"local-zfs:vm-11-disk-1","size_gb":10,"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},"cleared":false}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		err := runWorkspaceLeaseClear(context.Background(), []string{"ws-2"}, commonFlags{
			endpoint:   srv.URL,
			timeout:    time.Second,
			jsonOutput: false,
		})
		if err != nil {
			t.Fatalf("runWorkspaceLeaseClear() error = %v", err)
		}
	})

	if out != "workspace ops lease already clear\n" {
		t.Fatalf("stdout = %q", out)
	}
}
