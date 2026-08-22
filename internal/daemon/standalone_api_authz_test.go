package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/pool"
	"github.com/agentlab/agentlab/internal/user"
)

// TestStandaloneAPIAuthorization exercises the authorization gates on the
// APIs registered beside ControlAPI on the control mux: SecretsAPI (review
// F6), IntegrationAPI (review F11), UserAPI (review F13), and PoolAPI
// (review F12). Every route must refuse a zero-permission token, every
// mutation must refuse a sandbox-scoped token regardless of its commands,
// and each permission must work as an explicit grant for unscoped tokens.
func TestStandaloneAPIAuthorization(t *testing.T) {
	store := newTestStore(t)

	secretsStore, _ := newSecretsTestStore(t, true)
	intStore, err := integrations.NewStore(store, make([]byte, 32))
	if err != nil {
		t.Fatalf("new integration store: %v", err)
	}
	if err := intStore.Create(context.Background(), &integrations.Integration{
		Name:       "github",
		Type:       integrations.TypeGitProxy,
		Secret:     "ghp-test",
		Username:   "x-access-token",
		AttachMode: integrations.AttachAutoAll,
	}); err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	resourcePool := pool.New(pool.Config{TotalCores: 8, TotalMemoryMB: 8192})
	for _, alloc := range []struct {
		vmid, cores, mem int
		name             string
	}{
		{1001, 2, 2048, "sb-1001"},
		{1002, 4, 4096, "sb-1002"},
	} {
		if err := resourcePool.Allocate(alloc.vmid, alloc.name, "default", alloc.cores, alloc.mem, false); err != nil {
			t.Fatalf("seed pool allocation %d: %v", alloc.vmid, err)
		}
	}

	mux := http.NewServeMux()
	NewSecretsAPI(secretsStore, "default", nil, log.New(io.Discard, "", 0)).Register(mux)
	NewIntegrationAPI(intStore, log.New(io.Discard, "", 0)).Register(mux)
	NewUserAPI(user.NewRegistry(user.NewStore(store))).Register(mux)
	NewPoolAPI(resourcePool).Register(mux)

	doReq := func(t *testing.T, id *auth.RequestIdentity, method, path, body string) (int, []byte) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = bytes.NewBufferString(body)
		}
		req := httptest.NewRequest(method, path, rdr)
		rec := httptest.NewRecorder()
		if id != nil {
			mux.ServeHTTP(rec, req.WithContext(auth.WithIdentity(req.Context(), id)))
		} else {
			mux.ServeHTTP(rec, req)
		}
		return rec.Code, rec.Body.Bytes()
	}

	sshKey := func(t *testing.T) (line, fingerprint string) {
		t.Helper()
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate ed25519 key: %v", err)
		}
		parsed, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatalf("wrap ssh public key: %v", err)
		}
		return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed))), ssh.FingerprintSHA256(parsed)
	}

	// Identities under test.
	trusted := (*auth.RequestIdentity)(nil) // local Unix socket: no middleware
	fullAccess := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"*"}}}}
	zeroPerm := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"nothing"},
		Scope:    []string{"sandbox:1001"},
	}}}
	// scopedAll carries every command yet stays sandbox-scoped: global
	// resources must still refuse it.
	scopedAll := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"*"},
		Scope:    []string{"sandbox:1001"},
	}}}
	secretsReader := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"secrets.read"}}}}
	secretsWriter := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"secrets.write"}}}}
	integrationWriter := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"integration.read", "integration.write"}}}}
	integrationDeleter := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"integration.delete"}}}}
	userReader := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"user.read"}}}}
	userWriter := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"user.write"}}}}
	poolScoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"pool.status"},
		Scope:    []string{"sandbox:1001"},
	}}}

	// The deny-by-default matrix for these routes lives in api_authz_test.go.
	// It is built from the same registration set as the daemon's control mux,
	// so it covers ControlAPI, these standalone APIs, and the exec API in one
	// table (review T34). This file keeps the per-grant semantics.

	t.Run("sandbox-scoped tokens are refused on global resources", func(t *testing.T) {
		for _, id := range []*auth.RequestIdentity{scopedAll, zeroPerm} {
			if code, _ := doReq(t, id, http.MethodPut, "/v1/secrets/env", `{"env":{"K":"v"}}`); code != http.StatusForbidden {
				t.Errorf("scoped PUT /v1/secrets/env: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodGet, "/v1/secrets", ""); code != http.StatusForbidden {
				t.Errorf("scoped GET /v1/secrets: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodPost, "/v1/integrations", `{"name":"x","type":"http-proxy","target":"https://api.example.com","secret":"s","attach":"auto:all"}`); code != http.StatusForbidden {
				t.Errorf("scoped POST /v1/integrations: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodDelete, "/v1/integrations/github", ""); code != http.StatusForbidden {
				t.Errorf("scoped DELETE /v1/integrations/github: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodPost, "/v1/users", `{"name":"mallory","key":"k"}`); code != http.StatusForbidden {
				t.Errorf("scoped POST /v1/users: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodPost, "/v1/users/alice/keys", `{"key":"k"}`); code != http.StatusForbidden {
				t.Errorf("scoped POST /v1/users/alice/keys: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodDelete, "/v1/users/alice/keys?fingerprint=SHA256:x", ""); code != http.StatusForbidden {
				t.Errorf("scoped DELETE /v1/users/alice/keys: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodGet, "/v1/users", ""); code != http.StatusForbidden {
				t.Errorf("scoped GET /v1/users: got %d, want 403", code)
			}
			if code, _ := doReq(t, id, http.MethodPost, "/v1/teams", `{"name":"t"}`); code != http.StatusForbidden {
				t.Errorf("scoped POST /v1/teams: got %d, want 403", code)
			}
		}
	})

	t.Run("secrets permissions are per-grant", func(t *testing.T) {
		// Trusted and full-access callers may read and mutate.
		for _, id := range []*auth.RequestIdentity{trusted, fullAccess} {
			if code, _ := doReq(t, id, http.MethodPut, "/v1/secrets/env", `{"env":{"TRUSTED":"1"}}`); code != http.StatusOK {
				t.Errorf("trusted PUT /v1/secrets/env: got %d, want 200", code)
			}
			if code, _ := doReq(t, id, http.MethodGet, "/v1/secrets", ""); code != http.StatusOK {
				t.Errorf("trusted GET /v1/secrets: got %d, want 200", code)
			}
		}
		// secrets.read alone: read passes, mutate is refused.
		if code, _ := doReq(t, secretsReader, http.MethodGet, "/v1/secrets", ""); code != http.StatusOK {
			t.Errorf("secrets.read GET /v1/secrets: got %d, want 200", code)
		}
		if code, _ := doReq(t, secretsReader, http.MethodPut, "/v1/secrets/env", `{"env":{"K":"v"}}`); code != http.StatusForbidden {
			t.Errorf("secrets.read PUT /v1/secrets/env: got %d, want 403", code)
		}
		// secrets.write alone: mutate passes, read is refused.
		if code, body := doReq(t, secretsWriter, http.MethodPut, "/v1/secrets/env", `{"env":{"API_KEY":"sk-test"}}`); code != http.StatusOK {
			t.Fatalf("secrets.write PUT /v1/secrets/env: got %d body=%s, want 200", code, body)
		}
		if code, _ := doReq(t, secretsWriter, http.MethodGet, "/v1/secrets", ""); code != http.StatusForbidden {
			t.Errorf("secrets.write GET /v1/secrets: got %d, want 403", code)
		}
		if code, _ := doReq(t, secretsWriter, http.MethodDelete, "/v1/secrets/env/API_KEY", ""); code != http.StatusOK {
			t.Errorf("secrets.write DELETE /v1/secrets/env/API_KEY: got %d, want 200", code)
		}
	})

	t.Run("integration write cannot delete; delete is the elevated grant", func(t *testing.T) {
		// A write token lists and creates.
		if code, body := doReq(t, integrationWriter, http.MethodGet, "/v1/integrations", ""); code != http.StatusOK || !bytes.Contains(body, []byte("github")) {
			t.Fatalf("integration.read GET /v1/integrations: got %d body=%s, want 200 with github", code, body)
		}
		create := `{"name":"loop","type":"http-proxy","target":"https://api.example.com","secret":"sk-test","attach":"auto:all"}`
		if code, _ := doReq(t, integrationWriter, http.MethodPost, "/v1/integrations", create); code != http.StatusCreated {
			t.Fatalf("integration.write POST /v1/integrations: got %d, want 201", code)
		}
		// Duplicate names stay refused.
		if code, _ := doReq(t, integrationWriter, http.MethodPost, "/v1/integrations", create); code != http.StatusConflict {
			t.Errorf("duplicate POST /v1/integrations: got %d, want 409", code)
		}
		// Delete-then-recreate of an existing name needs the elevated grant:
		// bare integration.write cannot remove the live name.
		if code, _ := doReq(t, integrationWriter, http.MethodDelete, "/v1/integrations/loop", ""); code != http.StatusForbidden {
			t.Fatalf("integration.write DELETE /v1/integrations/loop: got %d, want 403", code)
		}
		// The delete grant performs the removal.
		if code, _ := doReq(t, integrationDeleter, http.MethodDelete, "/v1/integrations/loop", ""); code != http.StatusOK {
			t.Fatalf("integration.delete DELETE /v1/integrations/loop: got %d, want 200", code)
		}
		// With the name freed by the elevated grant, write may recreate it.
		if code, _ := doReq(t, integrationWriter, http.MethodPost, "/v1/integrations", create); code != http.StatusCreated {
			t.Errorf("recreate POST /v1/integrations after elevated delete: got %d, want 201", code)
		}
		// A delete-only token cannot create.
		if code, _ := doReq(t, integrationDeleter, http.MethodPost, "/v1/integrations", create); code != http.StatusForbidden {
			t.Errorf("integration.delete POST /v1/integrations: got %d, want 403", code)
		}
		// Nor read.
		if code, _ := doReq(t, integrationDeleter, http.MethodGet, "/v1/integrations", ""); code != http.StatusForbidden {
			t.Errorf("integration.delete GET /v1/integrations: got %d, want 403", code)
		}
	})

	t.Run("pool status is permission-gated and scope-filtered", func(t *testing.T) {
		// Trusted caller sees every allocation.
		code, body := doReq(t, trusted, http.MethodGet, "/v1/pool/status", "")
		if code != http.StatusOK {
			t.Fatalf("trusted GET /v1/pool/status: got %d, want 200", code)
		}
		var full V1PoolStatusResponse
		if err := json.Unmarshal(body, &full); err != nil {
			t.Fatalf("decode pool status: %v", err)
		}
		if full.ActiveCount != 2 || len(full.Allocations) != 2 {
			t.Fatalf("trusted pool status saw %d allocations, want 2", len(full.Allocations))
		}

		// A scoped token with pool.status sees only its own allocation and
		// aggregates recomputed from that subset.
		code, body = doReq(t, poolScoped, http.MethodGet, "/v1/pool/status", "")
		if code != http.StatusOK {
			t.Fatalf("scoped pool.status GET /v1/pool/status: got %d, want 200", code)
		}
		var scoped V1PoolStatusResponse
		if err := json.Unmarshal(body, &scoped); err != nil {
			t.Fatalf("decode scoped pool status: %v", err)
		}
		if len(scoped.Allocations) != 1 || scoped.Allocations[0].SandboxID != 1001 {
			t.Fatalf("scoped pool status allocations = %+v, want only sandbox 1001", scoped.Allocations)
		}
		if strings.Contains(string(body), "sb-1002") {
			t.Errorf("scoped pool status leaked out-of-scope allocation: %s", body)
		}
		if scoped.ActiveCount != 1 || scoped.AllocatedCores != 2 || scoped.AllocatedMemoryMB != 2048 {
			t.Errorf("scoped aggregates = %+v, want the single in-scope allocation", scoped)
		}

		// A full-access scoped token is still filtered.
		code, body = doReq(t, scopedAll, http.MethodGet, "/v1/pool/status", "")
		if code != http.StatusOK || strings.Contains(string(body), "sb-1002") {
			t.Errorf("scoped full-commands pool status: code=%d leaked=%v", code, strings.Contains(string(body), "sb-1002"))
		}

		// Unscoped tokens without pool.status are refused.
		if code, _ := doReq(t, secretsReader, http.MethodGet, "/v1/pool/status", ""); code != http.StatusForbidden {
			t.Errorf("secrets.read GET /v1/pool/status: got %d, want 403", code)
		}
	})

	t.Run("user and team permissions are per-grant", func(t *testing.T) {
		keyLine, fingerprint := sshKey(t)

		// Read grant lists but cannot mutate.
		if code, _ := doReq(t, userReader, http.MethodGet, "/v1/users", ""); code != http.StatusOK {
			t.Errorf("user.read GET /v1/users: got %d, want 200", code)
		}
		if code, _ := doReq(t, userReader, http.MethodGet, "/v1/teams", ""); code != http.StatusOK {
			t.Errorf("user.read GET /v1/teams: got %d, want 200", code)
		}
		if code, _ := doReq(t, userReader, http.MethodPost, "/v1/users", `{"name":"alice","key":"k"}`); code != http.StatusForbidden {
			t.Errorf("user.read POST /v1/users: got %d, want 403", code)
		}

		// Write grant mutates users and keys (T32 criterion).
		createBody, _ := json.Marshal(map[string]string{"name": "alice", "key": keyLine})
		if code, _ := doReq(t, userWriter, http.MethodPost, "/v1/users", string(createBody)); code != http.StatusCreated {
			t.Fatalf("user.write POST /v1/users: got %d, want 201", code)
		}
		if code, _ := doReq(t, userWriter, http.MethodPost, "/v1/users/alice/keys", `{"key":"`+keyLine+`"}`); code != http.StatusOK {
			t.Errorf("user.write POST /v1/users/alice/keys: got %d, want 200", code)
		}
		// Percent-encode the fingerprint: its base64 form may carry "+" or
		// "=", which a raw query string would not survive.
		if code, _ := doReq(t, userWriter, http.MethodDelete, "/v1/users/alice/keys?fingerprint="+url.QueryEscape(fingerprint), ""); code != http.StatusOK {
			t.Errorf("user.write DELETE /v1/users/alice/keys: got %d, want 200", code)
		}
		// Team mutations need the same grant.
		if code, _ := doReq(t, userWriter, http.MethodPost, "/v1/teams", `{"name":"team-a"}`); code != http.StatusCreated {
			t.Errorf("user.write POST /v1/teams: got %d, want 201", code)
		}
		// A write token cannot read the registry.
		if code, _ := doReq(t, userWriter, http.MethodGet, "/v1/users", ""); code != http.StatusForbidden {
			t.Errorf("user.write GET /v1/users: got %d, want 403", code)
		}
	})
}

// TestIntegrationAPITargetAllowlist verifies that a configured operator
// allowlist bounds integration targets at create time (review F11).
func TestIntegrationAPITargetAllowlist(t *testing.T) {
	store := newTestStore(t)
	intStore, err := integrations.NewStore(store, make([]byte, 32))
	if err != nil {
		t.Fatalf("new integration store: %v", err)
	}

	mux := http.NewServeMux()
	NewIntegrationAPI(intStore, log.New(io.Discard, "", 0)).
		WithTargetAllowlist([]string{"api.allowed.example", "10.1.2.3"}).
		Register(mux)

	doReq := func(method, path, body string) int {
		var rdr io.Reader
		if body != "" {
			rdr = bytes.NewBufferString(body)
		}
		req := httptest.NewRequest(method, path, rdr)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	create := func(name, target string) int {
		body, _ := json.Marshal(map[string]string{
			"name": name, "type": "http-proxy", "target": target,
			"secret": "sk-test", "attach": "auto:all",
		})
		return doReq(http.MethodPost, "/v1/integrations", string(body))
	}

	// The local socket is trusted; the allowlist still bounds the target.
	if code := create("allowed-host", "https://api.allowed.example/v1"); code != http.StatusCreated {
		t.Errorf("allowlist hit by hostname: got %d, want 201", code)
	}
	if code := create("allowed-ip", "http://10.1.2.3:8080"); code != http.StatusCreated {
		t.Errorf("allowlist hit by IP: got %d, want 201", code)
	}
	if code := create("miss", "https://evil.example.com"); code != http.StatusBadRequest {
		t.Errorf("allowlist miss: got %d, want 400", code)
	}
	if code := create("loopback", "http://127.0.0.1:8006"); code != http.StatusBadRequest {
		t.Errorf("loopback target: got %d, want 400", code)
	}
	if code := create("file", "file:///etc/passwd"); code != http.StatusBadRequest {
		t.Errorf("file scheme target: got %d, want 400", code)
	}
}
