package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

func TestSandboxStartStopHandlers(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      110,
		Name:      "handler-sb",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	startReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/110/start", nil)
	startRec := httptest.NewRecorder()
	api.handleSandboxByID(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want %d", startRec.Code, http.StatusOK)
	}
	if backend.startCalls != 1 {
		t.Fatalf("expected start to be called once, got %d", backend.startCalls)
	}
	var startResp V1SandboxResponse
	if err := json.NewDecoder(startRec.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.State != string(models.SandboxRunning) {
		t.Fatalf("expected running state, got %s", startResp.State)
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/110/stop", nil)
	stopRec := httptest.NewRecorder()
	api.handleSandboxByID(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d", stopRec.Code, http.StatusOK)
	}
	if backend.stopCalls != 1 {
		t.Fatalf("expected stop to be called once, got %d", backend.stopCalls)
	}
	var stopResp V1SandboxResponse
	if err := json.NewDecoder(stopRec.Body).Decode(&stopResp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if stopResp.State != string(models.SandboxStopped) {
		t.Fatalf("expected stopped state, got %s", stopResp.State)
	}
}

func TestSandboxStopAllHandlerMixedStates(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	sandboxes := []models.Sandbox{
		{VMID: 201, Name: "running", Profile: "default", State: models.SandboxRunning, CreatedAt: now},
		{VMID: 202, Name: "ready", Profile: "default", State: models.SandboxReady, CreatedAt: now},
		{VMID: 203, Name: "stopped", Profile: "default", State: models.SandboxStopped, CreatedAt: now},
		{VMID: 204, Name: "failed", Profile: "default", State: models.SandboxFailed, CreatedAt: now},
	}
	for _, sb := range sandboxes {
		if err := store.CreateSandbox(context.Background(), sb); err != nil {
			t.Fatalf("create sandbox: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/stop_all", nil)
	rec := httptest.NewRecorder()
	api.handleSandboxStopAll(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop_all status = %d, want %d", rec.Code, http.StatusOK)
	}
	if backend.stopCalls != 2 {
		t.Fatalf("expected stop called twice, got %d", backend.stopCalls)
	}
	var resp V1SandboxStopAllResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode stop_all response: %v", err)
	}
	if resp.Total != 4 {
		t.Fatalf("expected total 4, got %d", resp.Total)
	}
	if resp.Stopped != 2 {
		t.Fatalf("expected stopped 2, got %d", resp.Stopped)
	}
	if resp.Skipped != 2 {
		t.Fatalf("expected skipped 2, got %d", resp.Skipped)
	}
	if resp.Failed != 0 {
		t.Fatalf("expected failed 0, got %d", resp.Failed)
	}

	for _, vmid := range []int{201, 202} {
		sb, err := store.GetSandbox(context.Background(), vmid)
		if err != nil {
			t.Fatalf("get sandbox %d: %v", vmid, err)
		}
		if sb.State != models.SandboxStopped {
			t.Fatalf("expected sandbox %d stopped, got %s", vmid, sb.State)
		}
	}
}

func TestSandboxStartInvalidStateHandler(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      111,
		Name:      "invalid-state",
		Profile:   "default",
		State:     models.SandboxProvisioning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/111/start", bytes.NewBufferString("{}"))
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestSandboxCreateExplicitVMIDConflict(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {
			Name:       "default",
			TemplateVM: 9000,
			RawYAML:    "name: default\ntemplate_vmid: 9000\n",
		},
	}
	api := NewControlAPI(store, profiles, manager, nil, nil, "", log.New(io.Discard, "", 0))

	existing := models.Sandbox{
		VMID:      130,
		Name:      "existing",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), existing); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	job := models.Job{
		ID:        "job-vmid-conflict",
		RepoURL:   "https://example.com/repo.git",
		Ref:       "main",
		Profile:   "default",
		Status:    models.JobQueued,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"requested","profile":"default","vmid":130,"job_id":"job-vmid-conflict"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", body)
	rec := httptest.NewRecorder()
	api.handleSandboxes(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var resp V1ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "already exists") {
		t.Fatalf("expected already exists error, got %q", resp.Error)
	}

	sandboxes, err := store.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	if len(sandboxes) != 1 {
		t.Fatalf("expected exactly 1 sandbox row, got %d", len(sandboxes))
	}

	updatedJob, err := store.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updatedJob.SandboxVMID != nil {
		t.Fatalf("expected job sandbox_vmid to remain nil, got %d", *updatedJob.SandboxVMID)
	}
}

func TestSandboxRevertHandler(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      112,
		Name:      "revert-handler",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	body := bytes.NewBufferString(`{"restart": false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/112/revert", body)
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if backend.snapshotRollbackCalls != 1 {
		t.Fatalf("expected snapshot rollback once, got %d", backend.snapshotRollbackCalls)
	}
	var resp V1SandboxRevertResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode revert response: %v", err)
	}
	if resp.Sandbox.State != string(models.SandboxStopped) {
		t.Fatalf("expected stopped state, got %s", resp.Sandbox.State)
	}
	if resp.Restarted {
		t.Fatalf("expected no restart, got restarted=true")
	}
	if resp.Snapshot != cleanSnapshotName {
		t.Fatalf("expected snapshot %s, got %s", cleanSnapshotName, resp.Snapshot)
	}
}

func TestSandboxSnapshotCreateRequiresStopped(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      120,
		Name:      "snapshot-running",
		Profile:   "default",
		State:     models.SandboxRunning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"checkpoint-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/120/snapshots", body)
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var resp V1ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "stopped") {
		t.Fatalf("expected stopped error, got %q", resp.Error)
	}
}

func TestSandboxSnapshotCreateWorkspaceAttached(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	workspaceID := "ws-1"
	sandbox := models.Sandbox{
		VMID:        121,
		Name:        "snapshot-workspace",
		Profile:     "default",
		State:       models.SandboxStopped,
		Keepalive:   false,
		WorkspaceID: &workspaceID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"checkpoint-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/121/snapshots", body)
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var resp V1ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "workspace") {
		t.Fatalf("expected workspace error, got %q", resp.Error)
	}
}

func TestSandboxSnapshotRestoreMissing(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{snapshotRollbackErr: errors.New("snapshot not found")}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	sandbox := models.Sandbox{
		VMID:      122,
		Name:      "snapshot-missing",
		Profile:   "default",
		State:     models.SandboxStopped,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/122/snapshots/checkpoint-1/restore", nil)
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var resp V1ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "snapshot") {
		t.Fatalf("expected snapshot error, got %q", resp.Error)
	}
}

func TestSandboxTouchHandler(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{}, manager, nil, nil, "", log.New(io.Discard, "", 0))

	fixed := time.Date(2026, time.February, 8, 15, 0, 0, 0, time.UTC)
	api.now = func() time.Time { return fixed }

	sandbox := models.Sandbox{
		VMID:      113,
		Name:      "touch-handler",
		Profile:   "default",
		State:     models.SandboxRunning,
		Keepalive: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/113/touch", nil)
	rec := httptest.NewRecorder()
	api.handleSandboxByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp V1SandboxResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode touch response: %v", err)
	}
	if resp.LastUsedAt == nil {
		t.Fatalf("expected last_used_at to be set")
	}
	expected := fixed.UTC().Format(time.RFC3339Nano)
	if *resp.LastUsedAt != expected {
		t.Fatalf("expected last_used_at %s, got %s", expected, *resp.LastUsedAt)
	}

	got, err := store.GetSandbox(context.Background(), 113)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if !got.LastUsedAt.Equal(fixed.UTC()) {
		t.Fatalf("expected last_used_at %s, got %s", expected, got.LastUsedAt.UTC().Format(time.RFC3339Nano))
	}
}
