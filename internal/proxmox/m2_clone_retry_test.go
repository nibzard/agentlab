package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// --- Shell backend (synchronous clone) ---

// TestShellBackendCloneDestroysResidualBeforeRetry verifies that when a linked
// clone fails and leaves a partial VM at the target, the retry destroys that
// residual before reposting the full clone (review M2).
func TestShellBackendCloneDestroysResidualBeforeRetry(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{
		{err: errors.New("linked clone not possible: storage does not support snapshots")}, // clone (linked, fails)
		{stdout: "status: stopped\n"}, // qm status 101 → residual exists
		{},                           // qm destroy 101 --purge 1
		{},                           // clone (full, succeeds)
	}}
	backend := &ShellBackend{Runner: runner}

	if err := backend.Clone(context.Background(), 9000, 101, "sandbox-101"); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	want := []runnerCall{
		{name: "qm", args: []string{"clone", "9000", "101", "--full", "0", "--name", "sandbox-101"}},
		{name: "qm", args: []string{"status", "101"}},
		{name: "qm", args: []string{"destroy", "101", "--purge", "1"}},
		{name: "qm", args: []string{"clone", "9000", "101", "--full", "1", "--name", "sandbox-101"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Clone() calls = %#v, want %#v", runner.calls, want)
	}
}

// TestShellBackendCloneFullModeNotRetried verifies that an explicitly full clone
// is never retried as full again, even when the error looks snapshot-related
// (review M2).
func TestShellBackendCloneFullModeNotRetried(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{
		{err: errors.New("storage does not support snapshots")},
	}}
	backend := &ShellBackend{Runner: runner, CloneMode: "full"}

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err == nil {
		t.Fatal("expected clone error, got nil")
	}

	// Exactly one call: the full clone. No existence probe, no retry.
	want := []runnerCall{
		{name: "qm", args: []string{"clone", "9000", "101", "--full", "1", "--name", "sandbox-101"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Clone() calls = %#v, want %#v", runner.calls, want)
	}
}

// TestShellBackendCloneRetryAbortsOnProbeFailure verifies that if the residual
// probe itself errors (so we cannot prove what exists), the retry is aborted
// for operator action rather than blindly deleting or reposting (review M2).
func TestShellBackendCloneRetryAbortsOnProbeFailure(t *testing.T) {
	runner := &fakeRunner{responses: []runnerResponse{
		{err: errors.New("storage does not support snapshots")}, // clone (linked, fails)
		{err: errors.New("qm command timed out")},               // qm status → indeterminate
	}}
	backend := &ShellBackend{Runner: runner}

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err == nil {
		t.Fatal("expected retry-abort error, got nil")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("error %q should mention retry aborted", err.Error())
	}

	// No destroy, no full-clone repost.
	want := []runnerCall{
		{name: "qm", args: []string{"clone", "9000", "101", "--full", "0", "--name", "sandbox-101"}},
		{name: "qm", args: []string{"status", "101"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("Clone() calls = %#v, want %#v", runner.calls, want)
	}
}

// --- API backend (synchronous + asynchronous clone) ---

// newTestAPIBackend wires an APIBackend at an httptest server, recording every
// request after dispatching to h.
func newTestAPIBackend(t *testing.T, h http.HandlerFunc) (*APIBackend, *[]apiRequest) {
	t.Helper()
	var calls []apiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		form, _ := url.ParseQuery(string(body))
		calls = append(calls, apiRequest{method: r.Method, path: r.URL.Path, form: form})
		// Restore the body so handlers that call r.ParseForm() still see it.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	b := &APIBackend{BaseURL: srv.URL + "/api2/json", Node: "pve", HTTPClient: srv.Client()}
	return b, &calls
}

func cloneRequests(calls *[]apiRequest) []apiRequest { return *calls }

// TestAPIBackendCloneSyncFailureRetriesAsFull verifies a synchronous POST
// failure (snapshot error) is retried as a full clone, with no residual to
// clean up (review M2).
func TestAPIBackendCloneSyncFailureRetriesAsFull(t *testing.T) {
	backend, calls := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/9000/clone"):
			_ = r.ParseForm()
			if r.Form.Get("full") == "0" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":{"storage":"storage does not support snapshots"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{}}`)) // full retry succeeds, no UPID
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/qemu/101/status/current"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"vmid":"VM 101 does not exist"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if err := backend.Clone(context.Background(), 9000, 101, "sandbox-101"); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	// POST(linked, fail) → GET status (no residual) → POST(full, ok).
	got := cloneRequests(calls)
	if len(got) != 3 {
		t.Fatalf("calls = %d, want 3: %#v", len(got), got)
	}
	if got[0].form.Get("full") != "0" || got[2].form.Get("full") != "1" {
		t.Fatalf("expected linked-then-full, got full=%q then full=%q", got[0].form.Get("full"), got[2].form.Get("full"))
	}
}

// TestAPIBackendCloneAsyncFailureRetriesAsFull verifies that a failure reported
// asynchronously by waitForTask (after the POST returned a UPID) is also
// retried as a full clone, and the retry task is waited on (review M2).
func TestAPIBackendCloneAsyncFailureRetriesAsFull(t *testing.T) {
	backend, calls := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/9000/clone"):
			_ = r.ParseForm()
			if r.Form.Get("full") == "0" {
				_, _ = w.Write([]byte(`{"data":"UPID:pve:000ABC:linked"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":"UPID:pve:000DEF:full"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tasks/") && strings.HasSuffix(r.URL.Path, "/status"):
			if strings.Contains(r.URL.Path, "000ABC") {
				_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"storage does not support snapshots"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/qemu/101/status/current"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"vmid":"VM 101 does not exist"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if err := backend.Clone(context.Background(), 9000, 101, "sandbox-101"); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}

	// Both the linked and the full task status must have been polled.
	var sawLinkedTask, sawFullTask bool
	for _, c := range cloneRequests(calls) {
		if strings.Contains(c.path, "000ABC") {
			sawLinkedTask = true
		}
		if strings.Contains(c.path, "000DEF") {
			sawFullTask = true
		}
	}
	if !sawLinkedTask || !sawFullTask {
		t.Fatalf("expected both task-status polls, saw linked=%v full=%v", sawLinkedTask, sawFullTask)
	}
}

// TestAPIBackendCloneDestroysResidualBeforeRetry verifies a residual VM at the
// target is destroyed before the full-clone retry reposts to the same VMID
// (review M2).
func TestAPIBackendCloneDestroysResidualBeforeRetry(t *testing.T) {
	var sawDelete bool
	backend, calls := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/9000/clone"):
			_ = r.ParseForm()
			if r.Form.Get("full") == "0" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":{"storage":"storage does not support snapshots"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/qemu/101/status/current"):
			_, _ = w.Write([]byte(`{"data":{"status":"stopped"}}`)) // residual exists
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/qemu/101"):
			sawDelete = true
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if err := backend.Clone(context.Background(), 9000, 101, "sandbox-101"); err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if !sawDelete {
		t.Fatal("expected DELETE of residual target 101 before retry")
	}
	// The retry must repost as full after the destroy.
	var fullRepost bool
	for _, c := range cloneRequests(calls) {
		if c.method == http.MethodPost && strings.HasSuffix(c.path, "/qemu/9000/clone") && c.form.Get("full") == "1" {
			fullRepost = true
		}
	}
	if !fullRepost {
		t.Fatal("expected full-clone repost after residual cleanup")
	}
}

// TestAPIBackendCloneFullModeNotRetried verifies an explicitly full clone is not
// retried (and no existence probe happens) when it fails (review M2).
func TestAPIBackendCloneFullModeNotRetried(t *testing.T) {
	backend, calls := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/9000/clone") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"storage":"storage does not support snapshots"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	backend.CloneMode = "full"

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err == nil {
		t.Fatal("expected clone error, got nil")
	}
	got := cloneRequests(calls)
	if len(got) != 1 || got[0].form.Get("full") != "1" {
		t.Fatalf("expected a single full-clone POST, got %#v", got)
	}
}

// TestAPIBackendCloneRetryAlsoFailsPreservesBothErrors verifies that when the
// full-clone retry also fails, the returned error preserves both the original
// and the retry failure (review M2).
func TestAPIBackendCloneRetryAlsoFailsPreservesBothErrors(t *testing.T) {
	backend, _ := newTestAPIBackend(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/qemu/9000/clone"):
			_ = r.ParseForm()
			if r.Form.Get("full") == "0" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":{"storage":"storage does not support snapshots"}}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"storage":"disk creation failed"}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/qemu/101/status/current"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"vmid":"VM 101 does not exist"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := backend.Clone(context.Background(), 9000, 101, "sandbox-101")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "linked clone failed") || !strings.Contains(err.Error(), "full clone retry failed") {
		t.Fatalf("error %q should preserve both failures", err.Error())
	}
	if !strings.Contains(err.Error(), "disk creation failed") {
		t.Fatalf("error %q should include the retry failure detail", err.Error())
	}
}
