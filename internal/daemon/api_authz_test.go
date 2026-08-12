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

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

// TestControlAPI_Authorization exercises the network authorization boundary
// (review C1): every control route consults the authenticated identity, scoped
// SSH tokens are confined to their declared commands and sandbox scope, and the
// trusted path (no identity), legacy bearer token, and full-access SSH token
// remain unconstrained. It also confirms list filtering and the bulk/scope
// interaction.
func TestControlAPI_Authorization(t *testing.T) {
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, manager, workspaceMgr, nil, "", log.New(io.Discard, "", 0)).WithBackend(backend)

	ctx := context.Background()
	now := time.Now().UTC()
	for _, vmid := range []int{1001, 1002} {
		if err := store.CreateSandbox(ctx, models.Sandbox{
			VMID:          vmid,
			Name:          "sb",
			Profile:       "default",
			State:         models.SandboxRunning,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}); err != nil {
			t.Fatalf("create sandbox %d: %v", vmid, err)
		}
	}
	vm1001 := 1001
	if err := store.CreateJob(ctx, models.Job{
		ID:          "job-1001",
		RepoURL:     "https://example.com/r.git",
		Ref:         "main",
		Profile:     "default",
		Status:      models.JobRunning,
		SandboxVMID: &vm1001,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := store.CreateWorkspace(ctx, models.Workspace{
		ID:          "ws-1",
		Name:        "ws",
		Storage:     "local",
		VolumeID:    "vol-ws-1",
		SizeGB:      10,
		AttachedVM:  &vm1001,
		CreatedAt:   now,
		LastUpdated: now,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := store.CreateSession(ctx, models.Session{
		ID:          "sess-1",
		Name:        "sess",
		WorkspaceID: "ws-1",
		Profile:     "default",
		CurrentVMID: &vm1001,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, e := range []db.Exposure{
		{Name: "exp-1", VMID: 1001, Port: 8080, TargetIP: "10.77.0.2", State: "ready", CreatedAt: now, UpdatedAt: now},
		{Name: "exp-2", VMID: 1002, Port: 8081, TargetIP: "10.77.0.3", State: "ready", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateExposure(ctx, e); err != nil {
			t.Fatalf("create exposure %s: %v", e.Name, err)
		}
	}

	// Identities under test.
	trusted := (*auth.RequestIdentity)(nil)                 // local Unix socket: no middleware
	legacy := &auth.RequestIdentity{Method: "legacy-token"} // Token==nil → full access
	fullAccess := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"*"}}}}
	scopedRead := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox.read", "sandbox.list", "job.read", "workspace.read", "session.read", "exposure.list", "status.read"},
		Scope:    []string{"sandbox:1001"},
	}}}
	scopedSandbox := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox"},
		Scope:    []string{"sandbox:1001"},
	}}}
	zeroPerm := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"nothing"},
		Scope:    []string{"sandbox:1001"},
	}}}

	mux := http.NewServeMux()
	api.Register(mux)

	// doReq serves a request through the registered mux with the given identity
	// injected into the context (mirroring auth.WithIdentity in WrapNetwork). A
	// nil identity leaves the context untouched, as on the Unix socket.
	doReq := func(t *testing.T, id *auth.RequestIdentity, method, path string, body string) (int, []byte) {
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

	t.Run("trusted path is unconstrained", func(t *testing.T) {
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/v1/sandboxes/1001"},
			{http.MethodGet, "/v1/sandboxes/1002"},
			{http.MethodGet, "/v1/status"},
		} {
			if code, _ := doReq(t, trusted, tc.method, tc.path, ""); code != http.StatusOK {
				t.Errorf("trusted %s %s: got %d, want 200", tc.method, tc.path, code)
			}
		}
	})

	t.Run("legacy and full-access tokens are unconstrained", func(t *testing.T) {
		for _, id := range []*auth.RequestIdentity{legacy, fullAccess} {
			if code, _ := doReq(t, id, http.MethodGet, "/v1/sandboxes/1002", ""); code != http.StatusOK {
				t.Errorf("full-access GET /v1/sandboxes/1002: got %d, want 200", code)
			}
		}
	})

	t.Run("scoped read token: in-scope allowed, out-of-scope denied", func(t *testing.T) {
		if code, _ := doReq(t, scopedRead, http.MethodGet, "/v1/sandboxes/1001", ""); code != http.StatusOK {
			t.Errorf("in-scope GET /v1/sandboxes/1001: got %d, want 200", code)
		}
		if code, _ := doReq(t, scopedRead, http.MethodGet, "/v1/sandboxes/1002", ""); code != http.StatusForbidden {
			t.Errorf("out-of-scope GET /v1/sandboxes/1002: got %d, want 403", code)
		}
		// Indirect resources resolve to their sandbox: job/workspace/session on 1001 pass.
		for _, p := range []string{"/v1/jobs/job-1001", "/v1/workspaces/ws-1", "/v1/sessions/sess-1"} {
			if code, _ := doReq(t, scopedRead, http.MethodGet, p, ""); code != http.StatusOK {
				t.Errorf("in-scope GET %s: got %d, want 200", p, code)
			}
		}
		// A command the token lacks (sandbox.start) is denied even in-scope.
		if code, _ := doReq(t, scopedRead, http.MethodPost, "/v1/sandboxes/1001/start", ""); code != http.StatusForbidden {
			t.Errorf("sandbox.start (not granted) on in-scope sandbox: got %d, want 403", code)
		}
	})

	t.Run("scoped read token: list responses are filtered", func(t *testing.T) {
		code, body := doReq(t, scopedRead, http.MethodGet, "/v1/sandboxes", "")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/sandboxes: got %d, want 200", code)
		}
		var resp V1SandboxesResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode sandboxes: %v", err)
		}
		var seen []int
		for _, s := range resp.Sandboxes {
			seen = append(seen, s.VMID)
		}
		if !containsInt(seen, 1001) || containsInt(seen, 1002) {
			t.Errorf("scoped list saw %v, want only [1001]", seen)
		}

		// Exposure list is filtered by the bound sandbox: exp-1 (sandbox 1001,
		// in scope) is visible, exp-2 (sandbox 1002, out of scope) is hidden.
		ecode, ebody := doReq(t, scopedRead, http.MethodGet, "/v1/exposures", "")
		if ecode != http.StatusOK {
			t.Fatalf("GET /v1/exposures: got %d, want 200", ecode)
		}
		if !bytes.Contains(ebody, []byte("exp-1")) {
			t.Errorf("exposure list missing in-scope exp-1: %s", ebody)
		}
		if bytes.Contains(ebody, []byte("exp-2")) {
			t.Errorf("exposure list leaked out-of-scope exp-2: %s", ebody)
		}
	})

	t.Run("scoped sandbox-namespace token: bulk denied, out-of-scope denied", func(t *testing.T) {
		// Bulk operations are inherently cross-sandbox: scoped tokens are denied.
		if code, _ := doReq(t, scopedSandbox, http.MethodPost, "/v1/sandboxes/stop_all", ""); code != http.StatusForbidden {
			t.Errorf("scoped stop_all: got %d, want 403", code)
		}
		// Out-of-scope sandbox mutation denied before the backend is touched.
		if code, _ := doReq(t, scopedSandbox, http.MethodPost, "/v1/sandboxes/1002/start", ""); code != http.StatusForbidden {
			t.Errorf("out-of-scope start: got %d, want 403", code)
		}
		// In-scope read via the namespace grant is allowed.
		if code, _ := doReq(t, scopedSandbox, http.MethodGet, "/v1/sandboxes/1001", ""); code != http.StatusOK {
			t.Errorf("in-scope namespace read: got %d, want 200", code)
		}
	})

	t.Run("deny-by-default: zero-permission scoped token is refused everywhere", func(t *testing.T) {
		// Every readable route must refuse a token whose Commands match nothing.
		// A 200 here would indicate a route that skipped authorization.
		routes := []struct{ method, path string }{
			{http.MethodGet, "/v1/sandboxes/1001"},
			{http.MethodGet, "/v1/sandboxes"},
			{http.MethodGet, "/v1/sandboxes/inventory"},
			{http.MethodGet, "/v1/jobs/job-1001"},
			{http.MethodGet, "/v1/workspaces/ws-1"},
			{http.MethodGet, "/v1/workspaces"},
			{http.MethodGet, "/v1/sessions/sess-1"},
			{http.MethodGet, "/v1/sessions"},
			{http.MethodGet, "/v1/exposures"},
			{http.MethodDelete, "/v1/exposures/exp-1"},
			{http.MethodGet, "/v1/status"},
			{http.MethodGet, "/v1/host"},
			{http.MethodGet, "/v1/profiles"},
			{http.MethodGet, "/v1/schema"},
			{http.MethodPost, "/v1/sandboxes/stop_all"},
			{http.MethodPost, "/v1/sandboxes/prune"},
		}
		for _, r := range routes {
			if code, _ := doReq(t, zeroPerm, r.method, r.path, ""); code != http.StatusForbidden {
				t.Errorf("zero-perm %s %s: got %d, want 403 (route skipped authorization)", r.method, r.path, code)
			}
		}
	})
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
