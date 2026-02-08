package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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
