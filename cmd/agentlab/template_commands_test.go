package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tb "github.com/agentlab/agentlab/internal/templatebuild"
)

func TestTemplateCreateUsesDurableIdempotencyKey(t *testing.T) {
	var plan tb.Plan
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/template-builds" || r.Method != "POST" {
			t.Error("wrong route")
		}
		if e := json.NewDecoder(r.Body).Decode(&plan); e != nil {
			t.Fatal(e)
		}
		if plan.RequestID == "" || r.Header.Get("Idempotency-Key") != plan.RequestID {
			t.Error("missing idempotency key")
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(tb.Build{ID: "tb_test", VMID: 9010, State: "QUEUED", Plan: plan})
	}))
	defer server.Close()
	opts := commonFlags{endpoint: server.URL, allowInsecureHTTP: true, jsonOutput: true, timeout: time.Second}
	var err error
	out := captureStdout(t, func() {
		err = runTemplateCommand(context.Background(), []string{"create", "--name", "test", "--storage", "local"}, opts)
	})
	if err != nil {
		t.Fatal(err)
	}
	var b tb.Build
	if e := json.Unmarshal([]byte(out), &b); e != nil || b.ID != "tb_test" {
		t.Fatalf("not one JSON document: %q %v", out, e)
	}
}
func TestTemplateFailedWaitPrintsOneJSONDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(tb.Build{ID: "tb_failed", State: "FAILED", ErrorCode: "template_checksum_mismatch"})
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	var err error
	out := captureStdout(t, func() {
		err = templateWait(context.Background(), client, "tb_failed", time.Second, true)
		writeJSONError(os.Stdout, err)
	})
	var printed *printedJSONOnlyError
	if !errors.As(err, &printed) {
		t.Fatalf("failure lost exit status: %v", err)
	}
	var b tb.Build
	if e := json.Unmarshal([]byte(out), &b); e != nil || b.State != "FAILED" {
		t.Fatalf("multiple JSON documents: %q %v", out, e)
	}
}
func TestTemplateValidatePrintsStructuredFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_ = json.NewEncoder(w).Encode(tb.Validation{OK: false, Errors: []tb.Issue{{Code: "template_prerequisite_missing", Field: "qm", Message: "install prerequisites"}}})
	}))
	defer server.Close()
	opts := commonFlags{endpoint: server.URL, allowInsecureHTTP: true, jsonOutput: true, timeout: time.Second}
	var err error
	out := captureStdout(t, func() {
		err = runTemplateCommand(context.Background(), []string{"validate", "--name", "test", "--storage", "local"}, opts)
	})
	if err == nil || !strings.Contains(out, "template_prerequisite_missing") {
		t.Fatalf("validation report lost: %s %v", out, err)
	}
}
func TestTemplateLogsNDJSONAndExclusiveCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "4" {
			t.Error("cursor not forwarded")
		}
		_ = json.NewEncoder(w).Encode(tb.Logs{Records: []tb.Log{{Sequence: 5, Stage: "download", Message: "ok"}}, Next: 5})
	}))
	defer server.Close()
	c := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	var err error
	out := captureStdout(t, func() { err = templateLogs(context.Background(), c, "tb_test", 4, false, true) })
	var log tb.Log
	if e := json.Unmarshal([]byte(out), &log); err != nil || e != nil || log.Sequence != 5 {
		t.Fatalf("NDJSON: %s %v %v", out, err, e)
	}
}

func TestTemplateInvalidWaitAndListArgumentsNeverSubmit(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	opts := commonFlags{endpoint: server.URL, allowInsecureHTTP: true, timeout: time.Second}
	for _, args := range [][]string{{"create", "--name", "test", "--storage", "local", "--wait", "--wait-timeout", "0"}, {"build", "wait", "tb_test", "--timeout", "0"}, {"build", "list", "--limit", "0"}, {"list", "--limit", "0"}, {"build", "list", "--state", "INVALID"}} {
		if err := runTemplateCommand(context.Background(), args, opts); !errors.Is(err, errUsage) {
			t.Fatalf("invalid arguments did not return usage category: %v: %v", args, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid arguments made %d requests", requests)
	}
}
func TestTemplateOlderDaemonErrorsAreActionable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	opts := commonFlags{endpoint: server.URL, allowInsecureHTTP: true, timeout: time.Second}
	for _, args := range [][]string{{"capabilities"}, {"validate", "--name", "test", "--storage", "local"}, {"build", "show", "tb_test"}, {"build", "wait", "tb_test", "--timeout", "1s"}, {"build", "logs", "tb_test"}} {
		err := runTemplateCommand(context.Background(), args, opts)
		if err == nil || !strings.Contains(err.Error(), "upgrade agentlabd") {
			t.Fatalf("missing unsupported-feature advice: %v: %v", args, err)
		}
	}
}

func TestTemplateFollowCancellationPreservesCursorAndNDJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "0" {
			_ = json.NewEncoder(w).Encode(tb.Logs{Records: []tb.Log{{Sequence: 1, Timestamp: time.Now().UTC(), Stage: "download", Message: "first"}}, Next: 1})
			return
		}
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	client := newAPIClient(clientOptions{Endpoint: server.URL}, time.Second)
	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	defer func() { os.Stderr = originalStderr; reader.Close(); writer.Close() }()
	var followErr error
	output := captureStdout(t, func() {
		followErr = templateLogs(ctx, client, "tb_follow", 0, true, true)
		writeJSONError(os.Stdout, followErr)
	})
	os.Stderr = originalStderr
	_ = writer.Close()
	diagnostics, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(followErr, context.Canceled) {
		t.Fatalf("cancellation error lost: %v", followErr)
	}
	var record tb.Log
	if err = json.Unmarshal([]byte(output), &record); err != nil || record.Sequence != 1 {
		t.Fatalf("follow output contains non-log JSON or missing record: %q %v", output, err)
	}
	if !strings.Contains(string(diagnostics), "agentlab template build logs tb_follow --follow --after 1") {
		t.Fatalf("lost reconnect cursor: %s", diagnostics)
	}
}
