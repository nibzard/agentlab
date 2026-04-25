package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

func TestMetadataIndex(t *testing.T) {
	store := newTestStore(t)
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewMetadataAPI(store, secrets.Store{}, "", agentSubnet, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/", nil)
	req.RemoteAddr = "10.77.0.55:1234"
	resp := httptest.NewRecorder()
	api.handleIndex(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var decoded MetadataIndexResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Endpoints) == 0 {
		t.Fatal("expected non-empty endpoints")
	}
	found := false
	for _, ep := range decoded.Endpoints {
		if ep.Path == "/metadata/identity" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /metadata/identity endpoint in index")
	}
}

func TestMetadataIndex_SubnetOnly(t *testing.T) {
	store := newTestStore(t)
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewMetadataAPI(store, secrets.Store{}, "", agentSubnet, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/", nil)
	req.RemoteAddr = "192.168.1.2:1234"
	resp := httptest.NewRecorder()
	api.handleIndex(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.Code)
	}
}

func TestMetadataIndex_MethodNotAllowed(t *testing.T) {
	store := newTestStore(t)
	api := NewMetadataAPI(store, secrets.Store{}, "", nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/metadata/", nil)
	resp := httptest.NewRecorder()
	api.handleIndex(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.Code)
	}
}

func TestMetadataIdentity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:      3001,
		Name:      "metadata-test",
		Profile:   "default",
		State:     models.SandboxRunning,
		IP:        "10.77.1.42",
		CreatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.1.42"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewMetadataAPI(store, secrets.Store{}, "", agentSubnet, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.1.42:4321"
	resp := httptest.NewRecorder()
	api.handleIdentity(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var decoded MetadataIdentityResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.VMID != 3001 {
		t.Errorf("expected vmid 3001, got %d", decoded.VMID)
	}
	if decoded.Name != "metadata-test" {
		t.Errorf("expected name metadata-test, got %s", decoded.Name)
	}
	if decoded.Profile != "default" {
		t.Errorf("expected profile default, got %s", decoded.Profile)
	}
	if decoded.State != "RUNNING" {
		t.Errorf("expected state RUNNING, got %s", decoded.State)
	}
	if decoded.IP != "10.77.1.42" {
		t.Errorf("expected ip 10.77.1.42, got %s", decoded.IP)
	}
}

func TestMetadataIdentity_NotFound(t *testing.T) {
	store := newTestStore(t)
	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewMetadataAPI(store, secrets.Store{}, "", agentSubnet, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.9.99:4321"
	resp := httptest.NewRecorder()
	api.handleIdentity(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
}

func TestMetadataIdentity_WorkspaceID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	wsID := "ws-test-123"
	sandbox := models.Sandbox{
		VMID:        3002,
		Name:        "with-workspace",
		Profile:     "default",
		State:       models.SandboxRunning,
		IP:          "10.77.2.10",
		WorkspaceID: &wsID,
		CreatedAt:   now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.2.10"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	api := NewMetadataAPI(store, secrets.Store{}, "", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.2.10:4321"
	resp := httptest.NewRecorder()
	api.handleIdentity(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var decoded MetadataIdentityResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.WorkspaceID != "ws-test-123" {
		t.Errorf("expected workspace_id ws-test-123, got %s", decoded.WorkspaceID)
	}
}

func TestMetadataMetadata(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:      3003,
		Name:      "meta-sandbox",
		Profile:   "default",
		State:     models.SandboxRunning,
		IP:        "10.77.3.20",
		CreatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.3.20"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	// Create a secrets bundle with metadata.
	secretsDir := t.TempDir()
	bundlePath := filepath.Join(secretsDir, "default.yaml")
	bundle := []byte(`version: 1
metadata:
  environment: "production"
  team: "platform"
`)
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	api := NewMetadataAPI(store, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/metadata", nil)
	req.RemoteAddr = "10.77.3.20:4321"
	resp := httptest.NewRecorder()
	api.handleMetadata(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var decoded MetadataMetadataResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Metadata["environment"] != "production" {
		t.Errorf("expected environment=production, got %s", decoded.Metadata["environment"])
	}
	if decoded.Metadata["team"] != "platform" {
		t.Errorf("expected team=platform, got %s", decoded.Metadata["team"])
	}
	// Should also include sandbox identity metadata.
	if decoded.Metadata["sandbox_name"] != "meta-sandbox" {
		t.Errorf("expected sandbox_name=meta-sandbox, got %s", decoded.Metadata["sandbox_name"])
	}
	if decoded.Metadata["sandbox_vmid"] != "3003" {
		t.Errorf("expected sandbox_vmid=3003, got %s", decoded.Metadata["sandbox_vmid"])
	}
}

func TestMetadataSecrets(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:      3004,
		Name:      "secret-sandbox",
		Profile:   "default",
		State:     models.SandboxRunning,
		IP:        "10.77.4.30",
		CreatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.4.30"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	secretsDir := t.TempDir()
	bundlePath := filepath.Join(secretsDir, "default.yaml")
	bundle := []byte(`version: 1
env:
  API_KEY: "sk-secret123"
  DATABASE_URL: "postgres://localhost/db"
metadata:
  region: "us-east"
`)
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	api := NewMetadataAPI(store, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)

	// Test fetching env secret.
	req := httptest.NewRequest(http.MethodGet, "/metadata/secrets/API_KEY", nil)
	req.RemoteAddr = "10.77.4.30:4321"
	resp := httptest.NewRecorder()
	api.handleSecrets(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var decoded MetadataSecretResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Name != "API_KEY" {
		t.Errorf("expected name API_KEY, got %s", decoded.Name)
	}
	if decoded.Value != "sk-secret123" {
		t.Errorf("expected value sk-secret123, got %s", decoded.Value)
	}

	// Test fetching metadata secret.
	req2 := httptest.NewRequest(http.MethodGet, "/metadata/secrets/region", nil)
	req2.RemoteAddr = "10.77.4.30:4321"
	resp2 := httptest.NewRecorder()
	api.handleSecrets(resp2, req2)

	if resp2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp2.Code, resp2.Body.String())
	}
	var decoded2 MetadataSecretResponse
	if err := json.NewDecoder(resp2.Body).Decode(&decoded2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded2.Value != "us-east" {
		t.Errorf("expected value us-east, got %s", decoded2.Value)
	}
}

func TestMetadataSecrets_NotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	sandbox := models.Sandbox{
		VMID:    3005,
		Name:    "no-secret",
		Profile: "default",
		State:   models.SandboxRunning,
		IP:      "10.77.5.40",
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.5.40"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	secretsDir := t.TempDir()
	bundlePath := filepath.Join(secretsDir, "default.yaml")
	if err := os.WriteFile(bundlePath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	api := NewMetadataAPI(store, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/secrets/NONEXISTENT", nil)
	req.RemoteAddr = "10.77.5.40:4321"
	resp := httptest.NewRecorder()
	api.handleSecrets(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
}

func TestMetadataSecrets_EmptyName(t *testing.T) {
	store := newTestStore(t)
	api := NewMetadataAPI(store, secrets.Store{}, "", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/metadata/secrets/", nil)
	req.RemoteAddr = "10.77.0.1:4321"
	resp := httptest.NewRecorder()
	api.handleSecrets(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
}

func TestMetadataRateLimiting(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	sandbox := models.Sandbox{
		VMID:    3006,
		Name:    "rate-limited",
		Profile: "default",
		State:   models.SandboxRunning,
		IP:      "10.77.6.50",
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.UpdateSandboxIP(ctx, sandbox.VMID, "10.77.6.50"); err != nil {
		t.Fatalf("update sandbox ip: %v", err)
	}

	limiter := NewIPRateLimiter(1, 2)
	api := NewMetadataAPI(store, secrets.Store{}, "", mustParseCIDR(t, "10.77.0.0/16"), limiter, nil)

	// First two requests should succeed.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
		req.RemoteAddr = "10.77.6.50:4321"
		resp := httptest.NewRecorder()
		api.handleIdentity(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, resp.Code)
		}
	}

	// Third request should be rate limited.
	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.6.50:4321"
	resp := httptest.NewRecorder()
	api.handleIdentity(resp, req)
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.Code)
	}
}

func TestMetadataRegister(t *testing.T) {
	store := newTestStore(t)
	api := NewMetadataAPI(store, secrets.Store{}, "", nil, nil, nil)

	mux := http.NewServeMux()
	api.Register(mux)

	// Verify routes are registered by checking the mux handles them.
	routes := []string{"/metadata/", "/metadata/identity", "/metadata/metadata", "/metadata/secrets/"}
	for _, route := range routes {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		handler, pattern := mux.Handler(req)
		if handler == nil || pattern == "" {
			t.Errorf("expected route %s to be registered", route)
		}
	}
}

func TestMetadataRegister_NilMux(t *testing.T) {
	api := NewMetadataAPI(nil, secrets.Store{}, "", nil, nil, nil)
	api.Register(nil) // should not panic
}
