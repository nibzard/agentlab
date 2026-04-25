package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

// mockIPTablesRunner records iptables calls for test assertions.
type mockIPTablesRunner struct {
	calls [][]string
	err   error
}

func (m *mockIPTablesRunner) Run(args ...string) ([]byte, error) {
	m.calls = append(m.calls, args)
	if m.err != nil {
		return []byte("mock iptables error"), m.err
	}
	return nil, nil
}

func TestMetadataRouting_Setup(t *testing.T) {
	runner := &mockIPTablesRunner{}
	mr := NewMetadataRouting("10.77.0.1:8844")
	mr.runner = runner

	if err := mr.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !mr.active {
		t.Error("expected active=true after setup")
	}
	if len(runner.calls) < 1 {
		t.Fatal("expected at least 1 iptables call")
	}
	// First call is the delete (idempotent cleanup), second is the add.
	// Find the -A (add) call.
	var addCall []string
	for _, call := range runner.calls {
		for _, arg := range call {
			if arg == "-A" {
				addCall = call
				break
			}
		}
	}
	if addCall == nil {
		t.Fatal("expected an -A PREROUTING call")
	}
	// Verify the DNAT targets the correct address.
	found := false
	for i, arg := range addCall {
		if arg == "--to-destination" && i+1 < len(addCall) {
			if addCall[i+1] == "10.77.0.1:8844" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected --to-destination 10.77.0.1:8844 in add call, got %v", addCall)
	}
}

func TestMetadataRouting_Setup_Failure(t *testing.T) {
	runner := &mockIPTablesRunner{err: &stubError{"permission denied"}}
	mr := NewMetadataRouting("10.77.0.1:8844")
	mr.runner = runner

	if err := mr.Setup(); err == nil {
		t.Fatal("expected error from Setup with failing runner")
	}
	if mr.active {
		t.Error("expected active=false after failed setup")
	}
}

func TestMetadataRouting_Cleanup(t *testing.T) {
	runner := &mockIPTablesRunner{}
	mr := NewMetadataRouting("10.77.0.1:8844")
	mr.runner = runner

	// Setup first.
	if err := mr.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	callsBefore := len(runner.calls)

	// Cleanup.
	mr.Cleanup()
	if mr.active {
		t.Error("expected active=false after cleanup")
	}
	// Should have one more call (the -D delete).
	if len(runner.calls) != callsBefore+1 {
		t.Errorf("expected %d calls after cleanup, got %d", callsBefore+1, len(runner.calls))
	}
	// The last call should be a delete.
	lastCall := runner.calls[len(runner.calls)-1]
	hasDelete := false
	for _, arg := range lastCall {
		if arg == "-D" {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Errorf("expected -D in cleanup call, got %v", lastCall)
	}
}

func TestMetadataRouting_Cleanup_NotActive(t *testing.T) {
	runner := &mockIPTablesRunner{}
	mr := NewMetadataRouting("10.77.0.1:8844")
	mr.runner = runner

	// Cleanup without setup should be a no-op.
	mr.Cleanup()
	if len(runner.calls) != 0 {
		t.Errorf("expected 0 iptables calls, got %d", len(runner.calls))
	}
}

func TestMetadataRouting_NilReceiver(t *testing.T) {
	var mr *MetadataRouting
	// Should not panic.
	mr.Setup()
	mr.Cleanup()
}

type stubError struct {
	msg string
}

func (e *stubError) Error() string { return e.msg }

func TestMetadataAuditLog(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:      4001,
		Name:      "audit-test",
		Profile:   "default",
		State:     models.SandboxRunning,
		IP:        "10.77.1.100",
		CreatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.1.100"); err != nil {
		t.Fatalf("update ip: %v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	api := NewMetadataAPI(store, secrets.Store{}, "", mustParseCIDR(t, "10.77.0.0/16"), nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.1.100:4321"
	resp := httptest.NewRecorder()
	api.handleIdentity(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "audit-test") {
		t.Errorf("expected audit log to contain sandbox name, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "4001") {
		t.Errorf("expected audit log to contain vmid, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "10.77.1.100") {
		t.Errorf("expected audit log to contain IP, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "/metadata/identity") {
		t.Errorf("expected audit log to contain endpoint, got: %s", logOutput)
	}
}

func TestMetadataAuditLog_SecretsEndpoint(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	sandbox := models.Sandbox{
		VMID:    4002,
		Name:    "secret-audit",
		Profile: "default",
		State:   models.SandboxRunning,
		IP:      "10.77.2.200",
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.2.200"); err != nil {
		t.Fatalf("update ip: %v", err)
	}

	secretsDir := t.TempDir()
	bundlePath := secretsDir + "/default.yaml"
	bundle := []byte("version: 1\nenv:\n  MY_KEY: \"value123\"\n")
	if err := writeTestFile(bundlePath, bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	api := NewMetadataAPI(store, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/metadata/secrets/MY_KEY", nil)
	req.RemoteAddr = "10.77.2.200:4321"
	resp := httptest.NewRecorder()
	api.handleSecrets(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "secret-audit") {
		t.Errorf("expected audit log to contain sandbox name, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "MY_KEY") {
		t.Errorf("expected audit log to contain secret name, got: %s", logOutput)
	}
}

func TestMetadataIndexResponse_AuditFields(t *testing.T) {
	store := newTestStore(t)
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	api := NewMetadataAPI(store, secrets.Store{}, "", mustParseCIDR(t, "10.77.0.0/16"), nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/metadata/", nil)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()
	api.handleIndex(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	// Verify the response still has the correct structure.
	var decoded MetadataIndexResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Endpoints) == 0 {
		t.Error("expected endpoints in index")
	}
}

func writeTestFile(path string, data []byte) error {
	return writeFile(path, data)
}
