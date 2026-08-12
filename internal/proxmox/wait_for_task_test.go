package proxmox

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// instantSleep collapses waitForTask's backoff so polling tests run fast
// without real delays (review test-debt: async UPID coverage).
func instantSleep() func(context.Context, time.Duration) error {
	return func(context.Context, time.Duration) error { return nil }
}

// TestWaitForTask_PollsUntilStoppedOK proves a UPID that is still running on the
// first poll is polled again until it reports stopped with an OK exit status.
func TestWaitForTask_PollsUntilStoppedOK(t *testing.T) {
	var polls int32
	backend, _ := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tasks/UPID:ok/status") {
			n := atomic.AddInt32(&polls, 1)
			if n < 2 {
				_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	backend.Sleep = instantSleep()

	if err := backend.waitForTask(context.Background(), "pve", "UPID:ok"); err != nil {
		t.Fatalf("waitForTask: %v", err)
	}
	if got := atomic.LoadInt32(&polls); got < 2 {
		t.Fatalf("expected at least 2 polls, got %d", got)
	}
}

// TestWaitForTask_FailedExitStatus proves a task that stops with a non-OK
// exitstatus surfaces a descriptive error rather than nil (review test-debt).
func TestWaitForTask_FailedExitStatus(t *testing.T) {
	backend, _ := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"ERROR: VM is locked"}}`))
	})
	backend.Sleep = instantSleep()

	err := backend.waitForTask(context.Background(), "pve", "UPID:fail")
	if err == nil {
		t.Fatal("expected error for failed exitstatus, got nil")
	}
	if !strings.Contains(err.Error(), "exitstatus") || !strings.Contains(err.Error(), "ERROR: VM is locked") {
		t.Fatalf("error %q should mention exitstatus and the detail", err.Error())
	}
}

// TestWaitForTask_ContextCancel proves a cancelled context aborts polling with
// the context error rather than looping until the task stops on its own.
func TestWaitForTask_ContextCancel(t *testing.T) {
	backend, _ := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
	})
	backend.Sleep = func(ctx context.Context, _ time.Duration) error {
		// Simulate the context being cancelled during the wait between polls.
		return errors.New("context canceled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := backend.waitForTask(ctx, "pve", "UPID:slow")
	if err == nil {
		t.Fatal("expected error from cancelled wait, got nil")
	}
}

// TestWaitForTask_EmptyNodeOrUPIDSkipsPolling proves the guard clause: a missing
// node or UPID is treated as a synchronous (non-async) response and not polled.
func TestWaitForTask_EmptyNodeOrUPIDSkipsPolling(t *testing.T) {
	var polls int32
	backend, _ := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&polls, 1)
	})
	backend.Sleep = instantSleep()

	for _, tc := range []struct {
		name string
		node string
		upid string
	}{
		{"empty node", "", "UPID:x"},
		{"empty upid", "pve", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomic.StoreInt32(&polls, 0)
			if err := backend.waitForTask(context.Background(), tc.node, tc.upid); err != nil {
				t.Fatalf("waitForTask(%q,%q): %v", tc.node, tc.upid, err)
			}
			if got := atomic.LoadInt32(&polls); got != 0 {
				t.Fatalf("expected no polls for (%q,%q), got %d", tc.node, tc.upid, got)
			}
		})
	}
}
