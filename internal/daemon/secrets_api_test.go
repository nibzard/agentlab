package daemon

import (
	"bytes"
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

	"filippo.io/age"

	"github.com/agentlab/agentlab/internal/secrets"
)

func newSecretsTestStore(t *testing.T, withAgeKey bool) (secrets.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store := secrets.Store{Dir: dir, AllowPlaintext: false}
	if withAgeKey {
		identity, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("age identity: %v", err)
		}
		keyPath := filepath.Join(dir, "age.key")
		if err := os.WriteFile(keyPath, []byte(identity.String()+"\n"), 0o600); err != nil {
			t.Fatalf("write age key: %v", err)
		}
		store.AgeKeyPath = keyPath
	}
	return store, dir
}

func newSecretsAPIWithMux(store secrets.Store) (*SecretsAPI, *http.ServeMux) {
	api := NewSecretsAPI(store, "default", nil, log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	api.Register(mux)
	return api, mux
}

func doSecretsReq(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeSecretsResponse(t *testing.T, rr *httptest.ResponseRecorder) V1SecretsMutationResponse {
	t.Helper()
	var resp V1SecretsMutationResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response (code=%d, body=%s): %v", rr.Code, rr.Body.String(), err)
	}
	return resp
}

func TestSecretsAPIEnvRoundTrip(t *testing.T) {
	store, dir := newSecretsTestStore(t, true)
	_, mux := newSecretsAPIWithMux(store)

	rr := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/env", map[string]any{
		"env": map[string]string{
			"ANTHROPIC_API_KEY": "sk-ant-test-123",
			"OPENAI_API_KEY":    "sk-openai-test-456",
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT env: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sk-ant-test") || strings.Contains(rr.Body.String(), "sk-openai-test") {
		t.Fatalf("response leaked raw secret: %s", rr.Body.String())
	}
	resp := decodeSecretsResponse(t, rr)
	if len(resp.Secrets.Env) != 2 {
		t.Fatalf("expected 2 env keys, got %#v", resp.Secrets.Env)
	}
	if resp.Secrets.Env["ANTHROPIC_API_KEY"] != redactedValue {
		t.Fatalf("expected redacted env value, got %q", resp.Secrets.Env["ANTHROPIC_API_KEY"])
	}
	if !strings.HasSuffix(resp.Path, ".age") {
		t.Fatalf("expected age path, got %s", resp.Path)
	}

	// On disk: age-encrypted and decrypts to the staged values.
	data, err := os.ReadFile(filepath.Join(dir, "default.age"))
	if err != nil {
		t.Fatalf("read on-disk bundle: %v", err)
	}
	if !strings.HasPrefix(string(data), "age-encryption.org") {
		t.Fatalf("expected age-encrypted bundle on disk")
	}
	reload, err := secrets.Store{Dir: dir, AgeKeyPath: store.AgeKeyPath}.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("reload bundle: %v", err)
	}
	if reload.Env["ANTHROPIC_API_KEY"] != "sk-ant-test-123" {
		t.Fatalf("reload missing env value")
	}

	// GET reflects the staged (redacted) keys.
	getRR := doSecretsReq(t, mux, http.MethodGet, "/v1/secrets", nil)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET secrets: expected 200, got %d", getRR.Code)
	}
	getResp := decodeSecretsResponse(t, getRR)
	if len(getResp.Secrets.Env) != 2 {
		t.Fatalf("GET expected 2 env keys, got %#v", getResp.Secrets.Env)
	}

	// DELETE one env key.
	delRR := doSecretsReq(t, mux, http.MethodDelete, "/v1/secrets/env/ANTHROPIC_API_KEY", nil)
	if delRR.Code != http.StatusOK {
		t.Fatalf("DELETE env key: expected 200, got %d", delRR.Code)
	}
	delResp := decodeSecretsResponse(t, delRR)
	if _, ok := delResp.Secrets.Env["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("expected ANTHROPIC_API_KEY removed, got %#v", delResp.Secrets.Env)
	}
	if _, ok := delResp.Secrets.Env["OPENAI_API_KEY"]; !ok {
		t.Fatalf("expected OPENAI_API_KEY to remain")
	}
}

func TestSecretsAPIGitTailscaleSSH(t *testing.T) {
	store, _ := newSecretsTestStore(t, true)
	_, mux := newSecretsAPIWithMux(store)

	rr := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/git", map[string]any{
		"token":    "ghp_git-token-test",
		"username": "x-access-token",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT git: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "ghp_git-token-test") {
		t.Fatalf("response leaked git token: %s", rr.Body.String())
	}
	resp := decodeSecretsResponse(t, rr)
	if resp.Secrets.Git == nil || resp.Secrets.Git.Token != redactedValue || resp.Secrets.Git.Username != "x-access-token" {
		t.Fatalf("unexpected git view: %#v", resp.Secrets.Git)
	}

	// Tailscale set.
	tsRR := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/tailscale", map[string]any{
		"authkey":           "shared-auth-key-fixture",
		"hostname_template": "agentlab-{vmid}",
		"tags":              []string{"tag:agent"},
	})
	if tsRR.Code != http.StatusOK {
		t.Fatalf("PUT tailscale: expected 200, got %d body=%s", tsRR.Code, tsRR.Body.String())
	}
	if strings.Contains(tsRR.Body.String(), "shared-auth-key-fixture") {
		t.Fatalf("response leaked tailscale authkey: %s", tsRR.Body.String())
	}
	tsResp := decodeSecretsResponse(t, tsRR)
	if tsResp.Secrets.Tailscale == nil || !tsResp.Secrets.Tailscale.AuthKeyConfigured {
		t.Fatalf("expected tailscale configured: %#v", tsResp.Secrets.Tailscale)
	}
	if tsResp.Secrets.Tailscale.HostnameTemplate != "agentlab-{vmid}" {
		t.Fatalf("unexpected hostname template: %q", tsResp.Secrets.Tailscale.HostnameTemplate)
	}

	// Tailscale clear.
	clearRR := doSecretsReq(t, mux, http.MethodDelete, "/v1/secrets/tailscale", nil)
	if clearRR.Code != http.StatusOK {
		t.Fatalf("DELETE tailscale: expected 200, got %d", clearRR.Code)
	}
	clearResp := decodeSecretsResponse(t, clearRR)
	if clearResp.Secrets.Tailscale != nil {
		t.Fatalf("expected tailscale cleared, got %#v", clearResp.Secrets.Tailscale)
	}

	// SSH key add then remove.
	addRR := doSecretsReq(t, mux, http.MethodPost, "/v1/secrets/ssh-keys", map[string]any{
		"name": "laptop",
		"key":  "ssh-ed25519 AAAAC3NzaC1lZDI1 user@laptop",
	})
	if addRR.Code != http.StatusOK {
		t.Fatalf("POST ssh-key: expected 200, got %d body=%s", addRR.Code, addRR.Body.String())
	}
	addResp := decodeSecretsResponse(t, addRR)
	if addResp.Secrets.SSH == nil || addResp.Secrets.SSH["laptop"].Type != "ssh-ed25519" {
		t.Fatalf("expected ssh key, got %#v", addResp.Secrets.SSH)
	}

	remRR := doSecretsReq(t, mux, http.MethodDelete, "/v1/secrets/ssh-keys/laptop", nil)
	if remRR.Code != http.StatusOK {
		t.Fatalf("DELETE ssh-key: expected 200, got %d", remRR.Code)
	}
	remResp := decodeSecretsResponse(t, remRR)
	if len(remResp.Secrets.SSH) != 0 {
		t.Fatalf("expected ssh keys empty, got %#v", remResp.Secrets.SSH)
	}

	// Removing a missing key is 404.
	missingRR := doSecretsReq(t, mux, http.MethodDelete, "/v1/secrets/ssh-keys/ghost", nil)
	if missingRR.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing ssh-key: expected 404, got %d", missingRR.Code)
	}
}

func TestSecretsAPIErrors(t *testing.T) {
	store, _ := newSecretsTestStore(t, true)
	_, mux := newSecretsAPIWithMux(store)

	// set-tailscale with no authkey on an empty bundle → 400.
	rr := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/tailscale", map[string]any{
		"hostname_template": "agentlab-{vmid}",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing authkey, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Empty env body → 400.
	emptyRR := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/env", map[string]any{"env": map[string]string{}})
	if emptyRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty env, got %d", emptyRR.Code)
	}
}

func TestSecretsAPIPlaintextRefused(t *testing.T) {
	// No age key and plaintext disallowed: writing a new bundle resolves to a
	// .yaml path and must be refused by the shared Store.Mutate write policy.
	store, _ := newSecretsTestStore(t, false)
	_, mux := newSecretsAPIWithMux(store)

	rr := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/env", map[string]any{
		"env": map[string]string{"A": "1"},
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for plaintext refusal, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSecretsAPITailscaleAdminKey: an admin_api_key with no shared authkey is a
// valid (mint-only) configuration. It is persisted, never echoed, and surfaces
// only as a configured flag plus the non-secret tailnet.
func TestSecretsAPITailscaleAdminKey(t *testing.T) {
	store, _ := newSecretsTestStore(t, true)
	_, mux := newSecretsAPIWithMux(store)

	rr := doSecretsReq(t, mux, http.MethodPut, "/v1/secrets/tailscale", map[string]any{
		"admin_api_key": "admin-api-key-fixture",
		"tailnet":       "example.com",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT tailscale admin-only: expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "admin-api-key-fixture") {
		t.Fatalf("response leaked admin api key: %s", rr.Body.String())
	}
	resp := decodeSecretsResponse(t, rr)
	if resp.Secrets.Tailscale == nil {
		t.Fatal("expected tailscale view")
	}
	if !resp.Secrets.Tailscale.AdminAPIKeyConfigured {
		t.Fatal("expected AdminAPIKeyConfigured=true")
	}
	if resp.Secrets.Tailscale.AuthKeyConfigured {
		t.Fatal("expected AuthKeyConfigured=false for admin-only config")
	}
	if resp.Secrets.Tailscale.Tailnet != "example.com" {
		t.Fatalf("tailnet = %q want example.com", resp.Secrets.Tailscale.Tailnet)
	}

	// The admin key is persisted on disk (decrypts) and survives a reload.
	reload, err := secrets.Store{Dir: store.Dir, AgeKeyPath: store.AgeKeyPath}.Load(context.Background(), "default")
	if err != nil {
		t.Fatalf("reload bundle: %v", err)
	}
	if reload.GetTailscaleAdminAPIKey() != "admin-api-key-fixture" {
		t.Fatalf("admin key not persisted: %q", reload.GetTailscaleAdminAPIKey())
	}
	if !reload.TailscaleMintingConfigured() {
		t.Fatal("reloaded bundle reports minting unconfigured")
	}
}
