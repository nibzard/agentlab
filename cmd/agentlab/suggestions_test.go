package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRankSuggestions(t *testing.T) {
	got := rankSuggestions("sand", []string{"status", "sandbox", "sandstorm"}, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(got))
	}
	if got[0] != "sandbox" || got[1] != "sandstorm" {
		t.Fatalf("unexpected suggestions order: %v", got)
	}

	got = rankSuggestions("statua", []string{"sandbox", "status"}, 1)
	if len(got) != 1 || got[0] != "status" {
		t.Fatalf("expected status suggestion, got %v", got)
	}

	got = rankSuggestions("box", []string{"sandbox", "status"}, 1)
	if len(got) != 1 || got[0] != "sandbox" {
		t.Fatalf("expected sandbox suggestion, got %v", got)
	}
}

func TestNearestVMIDs(t *testing.T) {
	sandboxes := []sandboxResponse{
		{VMID: 105},
		{VMID: 101},
		{VMID: 99},
		{VMID: 103},
	}
	got := nearestVMIDs(100, sandboxes, 3)
	want := []int{99, 101, 103}
	if len(got) != len(want) {
		t.Fatalf("expected %d vmids, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("vmid %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestWrapSandboxNotFoundHints(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		resp := sandboxesResponse{Sandboxes: []sandboxResponse{{VMID: 99}, {VMID: 101}, {VMID: 150}}}
		writeJSON(t, w, http.StatusOK, resp)
	})

	socketPath := startUnixHTTPServer(t, mux)
	client := newAPIClient(clientOptions{SocketPath: socketPath}, time.Second)

	err := wrapSandboxNotFound(context.Background(), client, 100, errors.New("sandbox not found"))
	if err == nil {
		t.Fatalf("expected error")
	}
	msg, next, hints := describeError(err)
	if msg != "sandbox 100 not found" {
		t.Fatalf("unexpected message %q", msg)
	}
	if next != "agentlab sandbox list" {
		t.Fatalf("unexpected next %q", next)
	}
	joined := strings.Join(hints, " ")
	if !strings.Contains(joined, "closest VMIDs: 99, 101") {
		t.Fatalf("expected vmid hint, got %v", hints)
	}
}
