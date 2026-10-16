package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestStatusJSONPreservesGuestHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"api_schema_version":1,"event_schema_version":1,"sandbox_health":{"1234":{"healthy":false,"guest":{"ready":false,"error_code":"guest_not_ready"},"tailscale_state":"CONNECTED"}},"job_timelines":{"job1":{"status":"RUNNING"}},"recent_failure_digest":[{"kind":"sandbox.guest_readiness"}]}`))
	})
	socket := startUnixHTTPServer(t, mux)
	out := captureStdout(t, func() {
		if err := runStatusCommand(context.Background(), nil, commonFlags{socketPath: socket, jsonOutput: true, timeout: time.Second}); err != nil {
			t.Fatal(err)
		}
	})
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"sandbox_health", "job_timelines", "recent_failure_digest"} {
		if len(got[field]) == 0 {
			t.Fatalf("status discarded server diagnostics %s", field)
		}
	}
}
