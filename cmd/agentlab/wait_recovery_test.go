package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitTimeoutPreservesResumeIdentityWithoutCancellingWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/jobs/job-fixture" {
			t.Errorf("wait mutated or changed target: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"id":"job-fixture","status":"RUNNING"}`))
	}))
	defer server.Close()
	base := commonFlags{endpoint: server.URL, jsonOutput: true, timeout: 40 * time.Millisecond}
	out := captureStdout(t, func() {
		err := runWaitCommand(context.Background(), []string{"job-fixture"}, base, "job")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout: %v", err)
		}
		var detail *cliError
		if !errors.As(err, &detail) || !strings.Contains(strings.Join(detail.hints, " "), "agentlab job wait job-fixture") {
			t.Fatalf("resume identity lost: %+v", detail)
		}
	})
	if out != "" {
		t.Fatalf("partial JSON before timeout: %s", out)
	}
}
