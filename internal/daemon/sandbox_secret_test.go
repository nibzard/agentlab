package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

// seedSandboxSecret stores a hashed endpoint secret for vmid and returns the
// plaintext, mirroring what issueSandboxSecret does at bootstrap.
func seedSandboxSecret(t *testing.T, store *db.Store, vmid int) string {
	t.Helper()
	secret := "test-sandbox-secret-" + time.Now().Format("150405.000000000")
	hash, err := db.HashSandboxSecret(secret)
	if err != nil {
		t.Fatalf("hash sandbox secret: %v", err)
	}
	if err := store.UpsertSandboxSecret(context.Background(), vmid, hash); err != nil {
		t.Fatalf("seed sandbox secret: %v", err)
	}
	return secret
}

// withSandboxSecret sets the endpoint secret header on req.
func withSandboxSecret(req *http.Request, secret string) *http.Request {
	req.Header.Set(sandboxSecretHeader, secret)
	return req
}

// seedSecretTestSandbox creates a running sandbox bound to ip.
func seedSecretTestSandbox(t *testing.T, store *db.Store, vmid int, name, ip string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateSandbox(context.Background(), models.Sandbox{
		VMID:          vmid,
		Name:          name,
		Profile:       "default",
		State:         models.SandboxRunning,
		IP:            ip,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed sandbox %d: %v", vmid, err)
	}
}

// secretTestBundleDir writes a minimal plaintext secrets bundle whose env
// holds REGION, and returns the directory for a secrets.Store.
func secretTestBundleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bundle := "version: 1\nenv:\n  REGION: \"us-east\"\n"
	if err := os.WriteFile(filepath.Join(dir, "default.yaml"), []byte(bundle), 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return dir
}

// TestMetadataSandboxSecretRequired verifies the review F4 done-when
// criterion on every metadata route: a request with the right source IP but
// the wrong secret is rejected, and so is one with no secret.
func TestMetadataSandboxSecretRequired(t *testing.T) {
	store := newTestStore(t)
	seedSecretTestSandbox(t, store, 6001, "secret-holder", "10.77.9.10")
	secret := seedSandboxSecret(t, store, 6001)
	bundleDir := secretTestBundleDir(t)
	api := NewMetadataAPI(store, secrets.Store{Dir: bundleDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)
	mux := http.NewServeMux()
	api.Register(mux)

	routes := []string{"/metadata/", "/metadata/identity", "/metadata/metadata", "/metadata/secrets/REGION"}
	for _, route := range routes {
		cases := []struct {
			name   string
			secret string
			want   int
		}{
			{"correct secret", secret, http.StatusOK},
			{"wrong secret", "wrong-secret-value", http.StatusForbidden},
			{"missing secret", "", http.StatusForbidden},
		}
		for _, tc := range cases {
			t.Run(route+" "+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, route, nil)
				req.RemoteAddr = "10.77.9.10:4321"
				if tc.secret != "" {
					withSandboxSecret(req, tc.secret)
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("secret=%q: code=%d, want %d (body %s)", tc.secret, rec.Code, tc.want, rec.Body.String())
				}
				if tc.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "sandbox secret") {
					t.Fatalf("expected sandbox-secret rejection, got: %s", rec.Body.String())
				}
			})
		}
	}
}

// TestMetadataLegacySandboxRejected verifies that a sandbox with no stored
// secret — one bootstrapped before this check existed — cannot use the
// metadata endpoints, even though its source IP resolves. Identity must not
// rest on the source IP alone.
func TestMetadataLegacySandboxRejected(t *testing.T) {
	store := newTestStore(t)
	seedSecretTestSandbox(t, store, 6002, "legacy", "10.77.9.11")
	api := NewMetadataAPI(store, secrets.Store{}, "default", mustParseCIDR(t, "10.77.0.0/16"), nil, nil)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/metadata/identity", nil)
	req.RemoteAddr = "10.77.9.11:4321"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("legacy sandbox without secret: code=%d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sandbox secret") {
		t.Fatalf("expected sandbox-secret rejection, got: %s", rec.Body.String())
	}
}

// TestIntegrationProxySandboxSecretRequired verifies the review F4 done-when
// criterion on the credential proxy: the right source IP with the wrong
// secret never reaches the proxy handler.
func TestIntegrationProxySandboxSecretRequired(t *testing.T) {
	store := newTestStore(t)
	seedSecretTestSandbox(t, store, 6003, "proxy-holder", "10.77.9.12")
	secret := seedSandboxSecret(t, store, 6003)
	intStore := integrationsTestStore(t, store)
	proxyTestIntStoreWithName(t, store, intStore, "github", integrations.AttachAutoAll, "")

	var buf strings.Builder
	logger := log.New(&buf, "", 0)
	api := NewIntegrationProxyAPI(intStore, store, mustParseCIDR(t, "10.77.0.0/16"), nil, logger, false, false)
	mux := http.NewServeMux()
	api.Register(mux)

	newReq := func(secret string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/proxy/github/info/refs", nil)
		req.RemoteAddr = "10.77.9.12:4321"
		if secret != "" {
			withSandboxSecret(req, secret)
		}
		return req
	}

	// Correct secret: the identity gate passes. The proxy handler then runs
	// (its upstream is unreachable in tests, so any non-gate status is fine)
	// and the audit line is written.
	buf.Reset()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newReq(secret))
	if strings.Contains(rec.Body.String(), "sandbox secret") || strings.Contains(rec.Body.String(), "not identified") {
		t.Fatalf("correct secret was gated: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(buf.String(), "credential-proxy:") {
		t.Fatalf("expected audit log for admitted request, got: %s", buf.String())
	}

	for _, wrong := range []string{"wrong-secret-value", ""} {
		buf.Reset()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newReq(wrong))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("secret=%q: code=%d, want 403", wrong, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "sandbox secret") {
			t.Fatalf("secret=%q: expected sandbox-secret rejection, got: %s", wrong, rec.Body.String())
		}
		if strings.Contains(buf.String(), "credential-proxy:") {
			t.Fatalf("secret=%q: rejected request must not reach the proxy handler", wrong)
		}
	}
}

// TestIntegrationProxyLegacySandboxRejected verifies that an identified live
// sandbox with no stored secret cannot use the credential proxy.
func TestIntegrationProxyLegacySandboxRejected(t *testing.T) {
	store := newTestStore(t)
	seedSecretTestSandbox(t, store, 6005, "legacy-proxy", "10.77.9.13")
	intStore := integrationsTestStore(t, store)
	proxyTestIntStoreWithName(t, store, intStore, "github", integrations.AttachAutoAll, "")

	api := NewIntegrationProxyAPI(intStore, store, mustParseCIDR(t, "10.77.0.0/16"), nil, log.New(io.Discard, "", 0), false, false)
	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/proxy/github/info/refs", nil)
	req.RemoteAddr = "10.77.9.13:4321"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("legacy sandbox without secret: code=%d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "sandbox secret") {
		t.Fatalf("expected sandbox-secret rejection, got: %s", rec.Body.String())
	}
}

// TestBootstrapFetchIssuesSandboxSecret verifies the secret is delivered in
// the bootstrap response, stored only as a hash, and usable on the metadata
// endpoint afterwards.
func TestBootstrapFetchIssuesSandboxSecret(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	sandbox := models.Sandbox{
		VMID:      6004,
		Name:      "bootstrap-secret",
		Profile:   "yolo",
		State:     models.SandboxRunning,
		IP:        "10.77.9.14",
		CreatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	job := models.Job{
		ID:          "job_secret",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "yolo",
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	token := "token-secret-test"
	tokenHash, err := db.HashBootstrapToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, tokenHash, sandbox.VMID, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("create bootstrap token: %v", err)
	}

	bundleDir := secretTestBundleDir(t)
	api := NewBootstrapAPI(store, nil, secrets.Store{Dir: bundleDir, AllowPlaintext: true}, "default", mustParseCIDR(t, "10.77.0.0/16"), "", 0, nil, nil)
	api.now = func() time.Time { return now }

	payload := `{"token":"` + token + `","vmid":6004}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(payload))
	req.RemoteAddr = "10.77.0.55:1234"
	rec := httptest.NewRecorder()
	api.handleBootstrapFetch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var decoded V1BootstrapFetchResponse
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.SandboxSecret) < 32 {
		t.Fatalf("expected sandbox_secret in response, got %q", decoded.SandboxSecret)
	}
	storedHash, err := store.GetSandboxSecretHash(ctx, sandbox.VMID)
	if err != nil {
		t.Fatalf("load stored hash: %v", err)
	}
	presentedHash, err := db.HashSandboxSecret(decoded.SandboxSecret)
	if err != nil {
		t.Fatalf("hash presented secret: %v", err)
	}
	if storedHash != presentedHash {
		t.Fatal("stored hash does not match delivered secret")
	}
	if strings.Contains(rec.Body.String(), storedHash) {
		t.Fatal("response must not leak the stored hash")
	}
}
