package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

// fakeTailscaleMinter is a test stand-in for the Tailscale Admin API minter. It
// records whether it was invoked and what inputs it received, with no network.
type fakeTailscaleMinter struct {
	called  bool
	err     error
	key     string
	lastReq TailscaleMintRequest
}

func (f *fakeTailscaleMinter) MintAuthKey(_ context.Context, req TailscaleMintRequest) (TailscaleMintResult, error) {
	f.called = true
	f.lastReq = req
	if f.err != nil {
		return TailscaleMintResult{}, f.err
	}
	return TailscaleMintResult{Key: f.key, ID: "kFake", Expires: "2026-01-01T00:00:00Z"}, nil
}

// mintTestEnv stands up a bootstrap API wired to a sandbox + job + valid
// single-use token, backed by a secrets bundle containing the given tailscale
// YAML fragment. It returns the api (with the real default minter), the token
// and vmid for the caller to POST.
func mintTestEnv(t *testing.T, tailscaleYAML string) (*BootstrapAPI, string, int) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)

	const vmid = 5101
	sandbox := models.Sandbox{
		VMID:          vmid,
		Name:          "sandbox-mint",
		Profile:       "yolo",
		State:         models.SandboxRunning,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := store.CreateSandbox(ctx, sandbox); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	job := models.Job{
		ID:          "job_mint",
		RepoURL:     "https://example.com/repo.git",
		Ref:         "main",
		Profile:     "yolo",
		Task:        "run",
		Mode:        "dangerous",
		Status:      models.JobRunning,
		SandboxVMID: &sandbox.VMID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	token := "token-mint"
	hash, err := db.HashBootstrapToken(token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if err := store.CreateBootstrapToken(ctx, hash, sandbox.VMID, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("create bootstrap token: %v", err)
	}

	secretsDir := t.TempDir()
	bundle := []byte("version: 1\nenv:\n  OPENAI_API_KEY: \"sk-test\"\n" + tailscaleYAML)
	if err := os.WriteFile(filepath.Join(secretsDir, "default.yaml"), bundle, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	agentSubnet := mustParseCIDR(t, "10.77.0.0/16")
	api := NewBootstrapAPI(store, nil, secrets.Store{Dir: secretsDir, AllowPlaintext: true}, "default", agentSubnet, "", time.Hour, nil, nil)
	api.now = func() time.Time { return now }
	return api, token, vmid
}

func doMintFetch(t *testing.T, api *BootstrapAPI, token string, vmid int) *httptest.ResponseRecorder {
	t.Helper()
	payload := fmt.Sprintf(`{"token":%q,"vmid":%d}`, token, vmid)
	req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap/fetch", strings.NewReader(payload))
	req.RemoteAddr = "10.77.0.55:1234"
	rr := httptest.NewRecorder()
	api.handleBootstrapFetch(rr, req)
	return rr
}

// TestBootstrapMintsWhenAdminKeyPresent: with an admin api key configured the
// daemon mints a fresh per-VM key and delivers it (and only it) to the guest.
func TestBootstrapMintsWhenAdminKeyPresent(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
  tailnet: "example.com"
  tags:
    - "tag:agent"
`)
	minter := &fakeTailscaleMinter{key: "minted-auth-key-fixture"}
	api.tailscaleMinter = minter

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !minter.called {
		t.Fatal("expected minter to be invoked")
	}
	if minter.lastReq.AdminAPIKey != "admin-api-key-fixture" {
		t.Fatalf("admin key passed to minter = %q", minter.lastReq.AdminAPIKey)
	}
	if minter.lastReq.Tailnet != "example.com" {
		t.Fatalf("tailnet passed to minter = %q", minter.lastReq.Tailnet)
	}
	if minter.lastReq.Description != "agentlab vmid=5101" {
		t.Fatalf("description = %q", minter.lastReq.Description)
	}

	var resp V1BootstrapFetchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Tailscale == nil || resp.Tailscale.AuthKey != "minted-auth-key-fixture" {
		t.Fatalf("expected minted authkey in response, got %#v", resp.Tailscale)
	}
	if resp.Tailscale.Hostname != "agentlab-5101" {
		t.Fatalf("hostname = %q want agentlab-5101", resp.Tailscale.Hostname)
	}
	if len(resp.Tailscale.Tags) != 1 || resp.Tailscale.Tags[0] != "tag:agent" {
		t.Fatalf("tags = %#v", resp.Tailscale.Tags)
	}
	// The minted key rides the protected response, but the admin api key must
	// never appear in any response body.
	if strings.Contains(rr.Body.String(), "admin-api-key-fixture") {
		t.Fatalf("admin api key leaked into response: %s", rr.Body.String())
	}
}

// TestBootstrapMintFailsFallsBackToShared: a transient mint failure degrades to
// the stored shared auth key so the VM is not stranded.
func TestBootstrapMintFailsFallsBackToShared(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
  authkey: "shared-auth-fallback-fixture"
`)
	minter := &fakeTailscaleMinter{err: errors.New("admin api down")}
	api.tailscaleMinter = minter

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (fallback), got %d body=%s", rr.Code, rr.Body.String())
	}
	if !minter.called {
		t.Fatal("expected minter to be attempted before falling back")
	}
	var resp V1BootstrapFetchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Tailscale == nil || resp.Tailscale.AuthKey != "shared-auth-fallback-fixture" {
		t.Fatalf("expected shared fallback key, got %#v", resp.Tailscale)
	}
	if strings.Contains(rr.Body.String(), "admin-api-key-fixture") {
		t.Fatalf("admin api key leaked into fallback response: %s", rr.Body.String())
	}
}

// TestBootstrapMintFailsNoShared503: mint failure with no shared key to fall
// back to surfaces a 503, never the admin api key.
func TestBootstrapMintFailsNoShared503(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
`)
	minter := &fakeTailscaleMinter{err: errors.New("admin api down")}
	api.tailscaleMinter = minter

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "admin-api-key-fixture") {
		t.Fatalf("admin api key leaked into 503 body: %s", rr.Body.String())
	}
}

// TestBootstrapNoMintWhenNoCreds: with neither an admin key nor a shared key the
// response carries no tailscale block and the minter is never consulted.
func TestBootstrapNoMintWhenNoCreds(t *testing.T) {
	api, token, vmid := mintTestEnv(t, "")
	minter := &fakeTailscaleMinter{key: "skip-mint-fixture"}
	api.tailscaleMinter = minter

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if minter.called {
		t.Fatal("minter must not be called when minting is unconfigured")
	}
	var resp V1BootstrapFetchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Tailscale != nil {
		t.Fatalf("expected no tailscale block, got %#v", resp.Tailscale)
	}
}

// TestBootstrapLegacySharedKeyWithoutAdminKey: a shared key with no admin key
// takes the legacy path — the minter is bypassed and the stored key is served.
func TestBootstrapLegacySharedKeyWithoutAdminKey(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  authkey: "shared-auth-only-fixture"
  hostname_template: "agent-{vmid}"
`)
	minter := &fakeTailscaleMinter{key: "skip-mint-fixture"}
	api.tailscaleMinter = minter

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if minter.called {
		t.Fatal("minter must not be called on the legacy shared-key path")
	}
	var resp V1BootstrapFetchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Tailscale == nil || resp.Tailscale.AuthKey != "shared-auth-only-fixture" {
		t.Fatalf("expected shared key delivered, got %#v", resp.Tailscale)
	}
	if resp.Tailscale.Hostname != "agent-5101" {
		t.Fatalf("hostname = %q want agent-5101", resp.Tailscale.Hostname)
	}
}

// TestBootstrapMintHappensAfterConsume: minting is gated on the single-use token
// being consumed, so a replayed token mints no orphaned key.
func TestBootstrapMintHappensAfterConsume(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
`)
	minter := &fakeTailscaleMinter{key: "minted-auth-once-fixture"}
	api.tailscaleMinter = minter

	// First fetch: token valid → consumed → mints one key.
	first := doMintFetch(t, api, token, vmid)
	if first.Code != http.StatusOK {
		t.Fatalf("first fetch: expected 200, got %d body=%s", first.Code, first.Body.String())
	}
	if !minter.called {
		t.Fatal("expected minter called on first fetch")
	}

	// Replay the same single-use token: consume fails first, so the minter is
	// never reached again and no second key is minted.
	minter.called = false
	second := doMintFetch(t, api, token, vmid)
	if second.Code != http.StatusForbidden {
		t.Fatalf("replay: expected 403, got %d", second.Code)
	}
	if minter.called {
		t.Fatal("minter must not be called once the token is already consumed")
	}
}

// TestBootstrapMintingConfiguredButNilMinterDegradesGracefully guards the
// defensive path: if a future change leaves tailscaleMinter nil while an admin
// key is configured, the request must degrade to the shared key (or skip) with a
// warning rather than panic on a nil-interface method call.
func TestBootstrapMintingConfiguredButNilMinterDegradesGracefully(t *testing.T) {
	// With a shared key to fall back to: delivered, no panic, minter untouched.
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
  authkey: "shared-auth-key-fixture"
`)
	api.tailscaleMinter = nil
	api.logger = log.New(io.Discard, "", 0)

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (degrade), got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp V1BootstrapFetchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Tailscale == nil || resp.Tailscale.AuthKey != "shared-auth-key-fixture" {
		t.Fatalf("expected shared fallback key, got %#v", resp.Tailscale)
	}

	// With no shared key: nil Tailscale block (guest skips), still no panic.
	api2, token2, vmid2 := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
`)
	api2.tailscaleMinter = nil
	api2.logger = log.New(io.Discard, "", 0)

	rr2 := doMintFetch(t, api2, token2, vmid2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 (skip), got %d body=%s", rr2.Code, rr2.Body.String())
	}
	var resp2 V1BootstrapFetchResponse
	if err := json.NewDecoder(rr2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.Tailscale != nil {
		t.Fatalf("expected no tailscale block on nil-minter + no-shared, got %#v", resp2.Tailscale)
	}
}

// TestBootstrapMintRegistersKeyWithRedactor: the minted key value is registered
// with the redactor so it is scrubbed from any subsequent log line.
func TestBootstrapMintRegistersKeyWithRedactor(t *testing.T) {
	api, token, vmid := mintTestEnv(t, `
tailscale:
  admin_api_key: "admin-api-key-fixture"
`)
	minter := &fakeTailscaleMinter{key: "minted-auth-redact-fixture"}
	api.tailscaleMinter = minter
	redactor := NewRedactor(nil)
	api.redactor = redactor

	rr := doMintFetch(t, api, token, vmid)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	// The admin key was loaded from the bundle and registered too.
	if got := redactor.Redact("delivered admin-api-key-fixture to minter"); !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "admin-api-key-fixture") {
		t.Fatalf("admin key not redacted: %q", got)
	}
	// The freshly minted key is registered (transient, but logged paths must scrub it).
	if got := redactor.Redact("bootstrapped with minted-auth-redact-fixture"); !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "minted-auth-redact-fixture") {
		t.Fatalf("minted key not redacted: %q", got)
	}
}
