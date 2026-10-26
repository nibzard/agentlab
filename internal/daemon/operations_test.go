package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
)

type queuedOperationRunner struct{ fn func(context.Context) }

func (r *queuedOperationRunner) Go(_ string, fn func(context.Context)) bool { r.fn = fn; return true }
func (r *queuedOperationRunner) LifecycleContext() context.Context          { return context.Background() }

func TestCreateOperationReplayAndRecovery(t *testing.T) {
	plane := newAuthzPlane(t)
	runner := &queuedOperationRunner{}
	plane.api.runner = runner
	body := `{"profile":"default","job_id":"job-1001"}`
	create := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/sandboxes?async=true", strings.NewReader(payload))
		req.Header.Set("Idempotency-Key", "retry-key")
		rec := httptest.NewRecorder()
		plane.mux.ServeHTTP(rec, req)
		return rec
	}
	first := create(body)
	if first.Code != 202 {
		t.Fatalf("%d: %s", first.Code, first.Body.String())
	}
	var initial struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	replay := create(body)
	if replay.Code != 202 {
		t.Fatal(replay.Body.String())
	}
	conflict := create(`{"profile":"default","job_id":"job-1002"}`)
	if conflict.Code != 409 {
		t.Fatalf("want conflict: %s", conflict.Body.String())
	}
	runner.fn(context.Background())
	replay = create(body)
	if replay.Code != 200 {
		t.Fatal(replay.Body.String())
	}
	op, err := plane.api.store.GetOperation(context.Background(), initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.VMID == nil || op.State != "succeeded" {
		t.Fatalf("%+v", op)
	}
	sandboxes, err := plane.api.store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sandboxes) != 3 {
		t.Fatalf("duplicate allocation: %d sandboxes", len(sandboxes))
	}
	// A second API instance has no in-process work and must not allocate again.
	if err := plane.api.store.FinishOperation(context.Background(), op.ID, "accepted", 0, ""); err != nil {
		t.Fatal(err)
	}
	restarted := NewControlAPI(plane.api.store, nil, nil, nil, nil, "", nil)
	rec := httptest.NewRecorder()
	restarted.handleOperation(rec, httptest.NewRequest("GET", "/v1/operations/"+op.ID, nil))
	if !strings.Contains(rec.Body.String(), `"state":"unknown"`) || !strings.Contains(rec.Body.String(), `"vmid":`) {
		t.Fatal(rec.Body.String())
	}
	// A different caller cannot look up the operation.
	req := httptest.NewRequest("GET", "/v1/operations/"+op.ID, nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), &auth.RequestIdentity{Fingerprint: "other", Subject: "caller"}))
	rec = httptest.NewRecorder()
	restarted.handleOperation(rec, req)
	if rec.Code != 404 {
		t.Fatalf("cross-caller access: %d", rec.Code)
	}
}

func TestJobListPaginationAndScope(t *testing.T) {
	plane := newAuthzPlane(t)
	code, body := plane.serveAuthz(t, nil, "GET", "/v1/jobs?limit=1", "")
	if code != 200 {
		t.Fatal(string(body))
	}
	var page V1JobsResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 1 || page.NextCursor != "job-1001" {
		t.Fatalf("%+v", page)
	}
	code, body = plane.serveAuthz(t, nil, "GET", "/v1/jobs?limit=1&after="+page.NextCursor, "")
	if code != 200 {
		t.Fatal(string(body))
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 1 || page.Jobs[0].ID != "job-1002" {
		t.Fatalf("%+v", page)
	}
	id := &auth.RequestIdentity{Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.read"}, Scope: []string{"sandbox:1002"}}}}
	code, body = plane.serveAuthz(t, id, "GET", "/v1/jobs?limit=1", "")
	if code != 200 {
		t.Fatal(string(body))
	}
	var scoped V1JobsResponse
	if err := json.Unmarshal(body, &scoped); err != nil {
		t.Fatal(err)
	}
	if len(scoped.Jobs) != 1 || scoped.Jobs[0].ID != "job-1002" || scoped.NextCursor != "" {
		t.Fatalf("scope leaked: %+v", scoped)
	}
}

func TestPrunePreviewOnlyTimeouts(t *testing.T) {
	store := newTestStore(t)
	manager := NewSandboxManager(store, &stubBackend{}, nil)
	for i, state := range []models.SandboxState{models.SandboxTimeout, models.SandboxStopped} {
		if err := store.CreateSandbox(context.Background(), models.Sandbox{VMID: 100 + i, Name: "fixture", Profile: "default", State: state, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := manager.PruneTimedOut(context.Background(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Outcomes) != 1 || preview.Outcomes[0].VMID != 100 {
		t.Fatalf("%+v", preview)
	}
	result, err := manager.PruneTimedOut(context.Background(), false, map[int]bool{100: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || result.Outcomes[0].Outcome != "destroyed" {
		t.Fatalf("%+v", result)
	}
	stopped, err := store.GetSandbox(context.Background(), 101)
	if err != nil || stopped.State != models.SandboxStopped {
		t.Fatal("pruned stopped sandbox")
	}
}

func TestPruneReportsPartialFailuresAndChangedTargets(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{destroyErr: errors.New("backend unavailable")}
	manager := NewSandboxManager(store, backend, nil)
	for id, state := range map[int]models.SandboxState{2100: models.SandboxTimeout, 2101: models.SandboxStopped} {
		if err := store.CreateSandbox(ctx, models.Sandbox{VMID: id, Name: "fixture", Profile: "default", State: state}); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := manager.PruneTimedOut(ctx, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Outcomes) != 1 || preview.Outcomes[0].VMID != 2100 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	result, err := manager.PruneTimedOut(ctx, false, map[int]bool{2100: true, 2101: true, 9999: true})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[int]PruneOutcome{}
	for _, outcome := range result.Outcomes {
		outcomes[outcome.VMID] = outcome
	}
	if result.Count != 0 || len(outcomes) != 3 {
		t.Fatalf("unexpected results: %+v", result)
	}
	if outcomes[2100].Outcome != "failed" || !outcomes[2100].Retryable || outcomes[2100].Error == "" {
		t.Fatalf("lost retryable failure: %+v", outcomes[2100])
	}
	if outcomes[2101].Outcome != "skipped" || outcomes[2101].Retryable {
		t.Fatalf("changed target: %+v", outcomes[2101])
	}
	if outcomes[9999].Outcome != "not_found" || outcomes[9999].Retryable {
		t.Fatalf("missing target: %+v", outcomes[9999])
	}
	backend.destroyErr = nil
	retried, err := manager.PruneTimedOut(ctx, false, map[int]bool{2100: true})
	if err != nil || retried.Count != 1 {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	stopped, err := store.GetSandbox(ctx, 2101)
	if err != nil || stopped.State != models.SandboxStopped {
		t.Fatal("stopped sandbox changed")
	}
}

type gatedOperationRunner struct {
	registered  chan struct{}
	acknowledge chan struct{}
	finish      chan struct{}
	done        chan struct{}
	calls       atomic.Int64
}

func (r *gatedOperationRunner) LifecycleContext() context.Context { return context.Background() }
func (r *gatedOperationRunner) Go(_ string, fn func(context.Context)) bool {
	r.calls.Add(1)
	go func() { defer close(r.done); <-r.finish; fn(context.Background()) }()
	close(r.registered)
	<-r.acknowledge
	return true
}

func TestCreateOperationDroppedResponseAndConcurrentRetries(t *testing.T) {
	plane := newAuthzPlane(t)
	runner := &gatedOperationRunner{registered: make(chan struct{}), acknowledge: make(chan struct{}), finish: make(chan struct{}), done: make(chan struct{})}
	plane.api.runner = runner
	server := httptest.NewServer(plane.mux)
	defer server.Close()
	body := `{"profile":"default","job_id":"job-1001"}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/sandboxes?async=true", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Idempotency-Key", "lost-response")
	lost := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
		lost <- err
	}()
	select {
	case <-runner.registered:
	case <-time.After(5 * time.Second):
		t.Fatal("operation was not reserved")
	}
	// The operation exists, but the first HTTP response has not been sent yet.
	cancel()
	if err := <-lost; err == nil {
		t.Fatal("fixture failed to drop the first response")
	}
	close(runner.acknowledge)
	var workers sync.WaitGroup
	ids := make(chan string, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/sandboxes?async=true", strings.NewReader(body))
			if err != nil {
				failures <- err
				return
			}
			req.Header.Set("Idempotency-Key", "lost-response")
			response, err := server.Client().Do(req)
			if err != nil {
				failures <- err
				return
			}
			defer response.Body.Close()
			var operation V1OperationResponse
			if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
				failures <- err
				return
			}
			if response.StatusCode != 202 || operation.State != "accepted" {
				failures <- fmt.Errorf("retry returned %d state %s", response.StatusCode, operation.State)
				return
			}
			ids <- operation.ID
		}()
	}
	workers.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	id := ""
	count := 0
	for value := range ids {
		count++
		if id != "" && id != value {
			t.Fatal("retry allocated a second operation")
		}
		id = value
	}
	if count != 12 || runner.calls.Load() != 1 {
		t.Fatalf("replays=%d background calls=%d", count, runner.calls.Load())
	}
	close(runner.finish)
	select {
	case <-runner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not finish")
	}
	operation, err := plane.api.store.GetOperation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" || operation.VMID == nil {
		t.Fatalf("completion lost: %+v", operation)
	}
	sandboxes, err := plane.api.store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sandboxes) != 3 {
		t.Fatalf("duplicate allocation: %d", len(sandboxes))
	}
}

func TestPruneRejectsInvalidPreviewFlag(t *testing.T) {
	plane := newAuthzPlane(t)
	if err := plane.api.store.CreateSandbox(context.Background(), models.Sandbox{VMID: 2300, Name: "timed-out", Profile: "default", State: models.SandboxTimeout}); err != nil {
		t.Fatal(err)
	}
	code, body := plane.serveAuthz(t, nil, http.MethodPost, "/v1/sandboxes/prune?dry_run=tru", "")
	if code != http.StatusBadRequest {
		t.Fatalf("invalid preview flag executed: %d %s", code, body)
	}
	sandbox, err := plane.api.store.GetSandbox(context.Background(), 2300)
	if err != nil || sandbox.State != models.SandboxTimeout {
		t.Fatal("invalid preview request changed sandbox")
	}
}

func TestSandboxDetailsIdentifyLinkedWork(t *testing.T) {
	plane := newAuthzPlane(t)
	code, body := plane.serveAuthz(t, nil, http.MethodGet, "/v1/sandboxes/1001", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var sandbox V1SandboxResponse
	if err := json.Unmarshal(body, &sandbox); err != nil {
		t.Fatal(err)
	}
	if sandbox.JobID == nil || *sandbox.JobID != "job-1001" || sandbox.SessionID == nil || *sandbox.SessionID != "sess-1001" {
		t.Fatalf("linked workload missing: %+v", sandbox)
	}
}
