package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	execapi "github.com/agentlab/agentlab/internal/api"
	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/integrations"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/pool"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/agentlab/agentlab/internal/user"
)

// authzPlane is the control plane the daemon serves on its control mux. It is
// built from the same registration set as localMux in daemon.go: ControlAPI
// plus PoolAPI, SecretsAPI, IntegrationAPI, UserAPI, and the exec API. The
// deny-by-default matrix walks this registration set, so a route the daemon
// serves without an authorize call cannot escape the matrix (review T34).
type authzPlane struct {
	mux *http.ServeMux
	api *ControlAPI
}

// newAuthzPlane seeds two sandboxes (1001 in scope, 1002 out of scope) with a
// job, workspace, session, and exposure on each, plus pool allocations, an
// integration, and an unmanaged Proxmox VM. Every route then has a target on
// both sides of the scope boundary.
func newAuthzPlane(t *testing.T) *authzPlane {
	t.Helper()

	store := newTestStore(t)
	backend := &stubBackend{listVMs: []proxmox.VMSummary{
		{VMID: 1001, Name: "sb-1001", Status: proxmox.StatusRunning},
		{VMID: 1002, Name: "sb-1002", Status: proxmox.StatusRunning},
		{VMID: 2001, Name: "untracked", Status: proxmox.StatusStopped},
	}}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{
		"default": {Name: "default", TemplateVM: 9000, RawYAML: "name: default\ntemplate_vmid: 9000\n"},
	}
	ctrl := NewControlAPI(store, profiles, manager, workspaceMgr, nil, "", log.New(io.Discard, "", 0)).WithBackend(backend)

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
	for _, vmid := range []int{1001, 1002} {
		target := vmid
		if err := store.CreateJob(ctx, models.Job{
			ID:          "job-" + strconv.Itoa(vmid),
			RepoURL:     "https://example.com/r.git",
			Ref:         "main",
			Profile:     "default",
			Status:      models.JobRunning,
			SandboxVMID: &target,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("create job %d: %v", vmid, err)
		}
		if err := store.CreateWorkspace(ctx, models.Workspace{
			ID:          "ws-" + strconv.Itoa(vmid),
			Name:        "ws-" + strconv.Itoa(vmid),
			Storage:     "local",
			VolumeID:    "vol-" + strconv.Itoa(vmid),
			SizeGB:      10,
			AttachedVM:  &target,
			CreatedAt:   now,
			LastUpdated: now,
		}); err != nil {
			t.Fatalf("create workspace %d: %v", vmid, err)
		}
		if err := store.CreateSession(ctx, models.Session{
			ID:          "sess-" + strconv.Itoa(vmid),
			Name:        "sess-" + strconv.Itoa(vmid),
			WorkspaceID: "ws-" + strconv.Itoa(vmid),
			Profile:     "default",
			CurrentVMID: &target,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("create session %d: %v", vmid, err)
		}
		if err := store.CreateExposure(ctx, db.Exposure{
			Name:      "exp-" + strconv.Itoa(vmid),
			VMID:      vmid,
			Port:      8080,
			TargetIP:  "10.77.0.2",
			State:     "ready",
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("create exposure %d: %v", vmid, err)
		}
	}

	// Standalone APIs, registered exactly as daemon.go registers them.
	resourcePool := pool.New(pool.Config{TotalCores: 8, TotalMemoryMB: 8192})
	for _, vmid := range []int{1001, 1002} {
		if err := resourcePool.Allocate(vmid, "sb-"+strconv.Itoa(vmid), "default", 2, 2048, false); err != nil {
			t.Fatalf("seed pool allocation %d: %v", vmid, err)
		}
	}
	secretsStore, _ := newSecretsTestStore(t, true)
	intStore, err := integrations.NewStore(store, make([]byte, 32))
	if err != nil {
		t.Fatalf("new integration store: %v", err)
	}
	if err := intStore.Create(ctx, &integrations.Integration{
		Name:       "github",
		Type:       integrations.TypeGitProxy,
		Secret:     "ghp-test",
		Username:   "x-access-token",
		AttachMode: integrations.AttachAutoAll,
	}); err != nil {
		t.Fatalf("seed integration: %v", err)
	}

	mux := http.NewServeMux()
	ctrl.Register(mux)
	NewPoolAPI(resourcePool).Register(mux)
	NewSecretsAPI(secretsStore, "default", nil, log.New(io.Discard, "", 0)).Register(mux)
	NewIntegrationAPI(intStore, log.New(io.Discard, "", 0)).Register(mux)
	NewUserAPI(user.NewRegistry(user.NewStore(store))).Register(mux)
	// The CLI path never runs: a scoped token is refused by execAllowed before
	// the handler decodes the body.
	execapi.NewExecAPI("/nonexistent/agentlab", "/nonexistent/agentlab.sock", log.New(io.Discard, "", 0)).Register(mux)

	return &authzPlane{mux: mux, api: ctrl}
}

// serveAuthz serves one request through the plane mux with the given identity
// injected into the context, the way auth.WithIdentity does in WrapNetwork. A
// nil identity leaves the context untouched, as on the Unix socket.
func (p *authzPlane) serveAuthz(t *testing.T, id *auth.RequestIdentity, method, path, body string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	if id != nil {
		p.mux.ServeHTTP(rec, req.WithContext(auth.WithIdentity(req.Context(), id)))
	} else {
		p.mux.ServeHTTP(rec, req)
	}
	return rec.Code, rec.Body.Bytes()
}

// TestControlAPI_Authorization exercises the network authorization boundary
// (review C1): every control route consults the authenticated identity, scoped
// SSH tokens are confined to their declared commands and sandbox scope, and the
// trusted path (no identity), legacy bearer token, and full-access SSH token
// remain unconstrained. It also confirms list filtering and the bulk/scope
// interaction.
func TestControlAPI_Authorization(t *testing.T) {
	plane := newAuthzPlane(t)

	// Identities under test.
	trusted := (*auth.RequestIdentity)(nil)                 // local Unix socket: no middleware
	legacy := &auth.RequestIdentity{Method: "legacy-token"} // Token==nil → full access
	fullAccess := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"*"}}}}
	scopedRead := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"sandbox.read", "sandbox.list", "job.read", "workspace.read", "workspace.list", "session.read", "session.list", "exposure.list", "status.read", "pool.status", "message.read"},
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

	doReq := func(t *testing.T, id *auth.RequestIdentity, method, path string, body string) (int, []byte) {
		t.Helper()
		return plane.serveAuthz(t, id, method, path, body)
	}

	t.Run("trusted path is unconstrained", func(t *testing.T) {
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/v1/sandboxes/1001"},
			{http.MethodGet, "/v1/sandboxes/1002"},
			{http.MethodGet, "/v1/sandboxes/inventory"},
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
		for _, p := range []string{"/v1/jobs/job-1001", "/v1/workspaces/ws-1001", "/v1/sessions/sess-1001"} {
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

		// The inventory route filters the same way and drops unmanaged Proxmox
		// VMs, which no scope can cover (reviews F10 and T27).
		code, body = doReq(t, scopedRead, http.MethodGet, "/v1/sandboxes/inventory", "")
		if code != http.StatusOK {
			t.Fatalf("GET /v1/sandboxes/inventory: got %d, want 200", code)
		}
		var inventory V1SandboxInventoryResponse
		if err := json.Unmarshal(body, &inventory); err != nil {
			t.Fatalf("decode inventory: %v", err)
		}
		var inventoryVMIDs []int
		for _, entry := range inventory.Sandboxes {
			inventoryVMIDs = append(inventoryVMIDs, entry.VMID)
			if !entry.Managed {
				t.Errorf("scoped inventory returned unmanaged record %+v", entry)
			}
		}
		if !containsInt(inventoryVMIDs, 1001) || containsInt(inventoryVMIDs, 1002) || containsInt(inventoryVMIDs, 2001) {
			t.Errorf("scoped inventory saw %v, want only [1001]", inventoryVMIDs)
		}

		// Exposure list is filtered by the bound sandbox: exp-1 (sandbox 1001,
		// in scope) is visible, exp-2 (sandbox 1002, out of scope) is hidden.
		ecode, ebody := doReq(t, scopedRead, http.MethodGet, "/v1/exposures", "")
		if ecode != http.StatusOK {
			t.Fatalf("GET /v1/exposures: got %d, want 200", ecode)
		}
		if !bytes.Contains(ebody, []byte("exp-1001")) {
			t.Errorf("exposure list missing in-scope exp-1001: %s", ebody)
		}
		if bytes.Contains(ebody, []byte("exp-1002")) {
			t.Errorf("exposure list leaked out-of-scope exp-1002: %s", ebody)
		}

		// Workspace and session lists drop records that resolve to an
		// out-of-scope sandbox.
		wcode, wbody := doReq(t, scopedRead, http.MethodGet, "/v1/workspaces", "")
		if wcode != http.StatusOK {
			t.Fatalf("GET /v1/workspaces: got %d, want 200", wcode)
		}
		if bytes.Contains(wbody, []byte("ws-1002")) {
			t.Errorf("workspace list leaked out-of-scope ws-1002: %s", wbody)
		}
		scode, sbody := doReq(t, scopedRead, http.MethodGet, "/v1/sessions", "")
		if scode != http.StatusOK {
			t.Fatalf("GET /v1/sessions: got %d, want 200", scode)
		}
		if bytes.Contains(sbody, []byte("sess-1002")) {
			t.Errorf("session list leaked out-of-scope sess-1002: %s", sbody)
		}

		// Pool status narrows its allocations and recomputes the aggregates
		// from the surviving subset (review F12).
		pcode, pbody := doReq(t, scopedRead, http.MethodGet, "/v1/pool/status", "")
		if pcode != http.StatusOK {
			t.Fatalf("GET /v1/pool/status: got %d, want 200", pcode)
		}
		var poolStatus V1PoolStatusResponse
		if err := json.Unmarshal(pbody, &poolStatus); err != nil {
			t.Fatalf("decode pool status: %v", err)
		}
		if poolStatus.ActiveCount != 1 || len(poolStatus.Allocations) != 1 || poolStatus.Allocations[0].SandboxID != 1001 {
			t.Errorf("scoped pool status = %+v, want the single allocation for 1001", poolStatus)
		}
		if bytes.Contains(pbody, []byte("sb-1002")) {
			t.Errorf("pool status leaked out-of-scope allocation: %s", pbody)
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

	// T34: the matrix is built from the daemon's registration set, so it covers
	// the standalone APIs and the exec API too. A 200 here means a route
	// skipped authorization; a 404 or 405 means the route is not registered as
	// the daemon registers it.
	t.Run("deny-by-default: zero-permission token is refused everywhere", func(t *testing.T) {
		routes := []struct{ method, path, body string }{
			{http.MethodPost, "/v1/jobs", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t"}`},
			{http.MethodPost, "/v1/jobs/validate-plan", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t"}`},
			{http.MethodGet, "/v1/jobs/job-1001", ""},
			{http.MethodGet, "/v1/jobs/job-1001/artifacts", ""},
			{http.MethodGet, "/v1/jobs/job-1001/artifacts/download?path=out.txt", ""},
			{http.MethodPost, "/v1/jobs/job-1001/doctor", ""},
			{http.MethodGet, "/v1/profiles", ""},
			{http.MethodGet, "/v1/schema", ""},
			{http.MethodGet, "/v1/status", ""},
			{http.MethodGet, "/v1/host", ""},
			{http.MethodPost, "/v1/messages", `{"scope_type":"workspace","scope_id":"ws-1001","text":"hi"}`},
			{http.MethodGet, "/v1/messages?scope_type=workspace&scope_id=ws-1001", ""},
			{http.MethodGet, "/v1/sandboxes", ""},
			{http.MethodPost, "/v1/sandboxes", `{"profile":"default"}`},
			{http.MethodGet, "/v1/sandboxes/inventory", ""},
			{http.MethodPost, "/v1/sandboxes/validate-plan", `{"profile":"default"}`},
			{http.MethodPost, "/v1/sandboxes/reconcile", `{}`},
			{http.MethodPost, "/v1/sandboxes/stop_all", ""},
			{http.MethodPost, "/v1/sandboxes/prune", ""},
			{http.MethodGet, "/v1/sandboxes/1001", ""},
			{http.MethodPost, "/v1/sandboxes/1001/start", ""},
			{http.MethodPost, "/v1/sandboxes/1001/stop", ""},
			{http.MethodPost, "/v1/sandboxes/1001/pause", ""},
			{http.MethodPost, "/v1/sandboxes/1001/resume", ""},
			{http.MethodPost, "/v1/sandboxes/1001/update", `{}`},
			{http.MethodPost, "/v1/sandboxes/1001/touch", ""},
			{http.MethodPost, "/v1/sandboxes/1001/revert", ""},
			{http.MethodPost, "/v1/sandboxes/1001/destroy", ""},
			{http.MethodGet, "/v1/sandboxes/1001/snapshots", ""},
			{http.MethodPost, "/v1/sandboxes/1001/snapshots", `{"name":"snap"}`},
			{http.MethodPost, "/v1/sandboxes/1001/snapshots/snap/restore", ""},
			{http.MethodGet, "/v1/sandboxes/1001/events", ""},
			{http.MethodPost, "/v1/sandboxes/1001/doctor", ""},
			{http.MethodPost, "/v1/sandboxes/1001/lease/renew", `{}`},
			{http.MethodGet, "/v1/workspaces", ""},
			{http.MethodPost, "/v1/workspaces", `{"name":"ws","size_gb":1}`},
			{http.MethodGet, "/v1/workspaces/ws-1001", ""},
			{http.MethodGet, "/v1/workspaces/ws-1001/check", ""},
			{http.MethodPost, "/v1/workspaces/ws-1001/fsck", ""},
			{http.MethodGet, "/v1/workspaces/ws-1001/snapshots", ""},
			{http.MethodPost, "/v1/workspaces/ws-1001/snapshots", `{"name":"snap"}`},
			{http.MethodPost, "/v1/workspaces/ws-1001/snapshots/snap/restore", ""},
			{http.MethodPost, "/v1/workspaces/ws-1001/attach", `{"vmid":1001}`},
			{http.MethodPost, "/v1/workspaces/ws-1001/detach", ""},
			{http.MethodPost, "/v1/workspaces/ws-1001/rebind", `{"profile":"default"}`},
			{http.MethodPost, "/v1/workspaces/ws-1001/fork", `{"name":"ws-copy"}`},
			{http.MethodPost, "/v1/workspaces/ws-1001/lease/clear", ""},
			{http.MethodGet, "/v1/sessions", ""},
			{http.MethodPost, "/v1/sessions", `{"name":"s","profile":"default","workspace_id":"ws-1001"}`},
			{http.MethodGet, "/v1/sessions/sess-1001", ""},
			{http.MethodPost, "/v1/sessions/sess-1001/resume", ""},
			{http.MethodPost, "/v1/sessions/sess-1001/stop", ""},
			{http.MethodPost, "/v1/sessions/sess-1001/fork", `{"name":"s-copy"}`},
			{http.MethodPost, "/v1/sessions/sess-1001/doctor", ""},
			{http.MethodGet, "/v1/exposures", ""},
			{http.MethodPost, "/v1/exposures", `{"name":"probe","vmid":1001,"port":8080}`},
			{http.MethodDelete, "/v1/exposures/exp-1001", ""},
			// Standalone APIs registered beside ControlAPI on the same mux.
			{http.MethodGet, "/v1/secrets", ""},
			{http.MethodPut, "/v1/secrets/env", `{"env":{"K":"v"}}`},
			{http.MethodDelete, "/v1/secrets/env/KEY", ""},
			{http.MethodPut, "/v1/secrets/git", `{}`},
			{http.MethodPut, "/v1/secrets/tailscale", `{}`},
			{http.MethodDelete, "/v1/secrets/tailscale", ""},
			{http.MethodPost, "/v1/secrets/ssh-keys", `{"key":"k"}`},
			{http.MethodDelete, "/v1/secrets/ssh-keys/k", ""},
			{http.MethodGet, "/v1/integrations", ""},
			{http.MethodPost, "/v1/integrations", `{"name":"x","type":"http-proxy","target":"https://api.example.com","secret":"s","attach":"auto:all"}`},
			{http.MethodGet, "/v1/integrations/github", ""},
			{http.MethodDelete, "/v1/integrations/github", ""},
			{http.MethodGet, "/v1/users", ""},
			{http.MethodPost, "/v1/users", `{"name":"alice","key":"k"}`},
			{http.MethodGet, "/v1/users/alice", ""},
			{http.MethodDelete, "/v1/users/alice", ""},
			{http.MethodPost, "/v1/users/alice/keys", `{"key":"k"}`},
			{http.MethodDelete, "/v1/users/alice/keys?fingerprint=SHA256:x", ""},
			{http.MethodGet, "/v1/teams", ""},
			{http.MethodPost, "/v1/teams", `{"name":"team-a"}`},
			{http.MethodDelete, "/v1/teams/team-a", ""},
			{http.MethodGet, "/v1/teams/team-a/members", ""},
			{http.MethodPost, "/v1/teams/team-a/members", `{"name":"alice"}`},
			{http.MethodDelete, "/v1/teams/team-a/members/alice", ""},
			{http.MethodGet, "/v1/pool/status", ""},
			{http.MethodPost, "/v1/exec", `{"command":"sandbox list"}`},
			{http.MethodPost, "/v1/exec/dry-run", `{"command":"sandbox list"}`},
		}
		for _, rt := range routes {
			if code, body := doReq(t, zeroPerm, rt.method, rt.path, rt.body); code != http.StatusForbidden {
				t.Errorf("zero-perm %s %s: got %d, want 403 (route skipped authorization; body %s)", rt.method, rt.path, code, body)
			}
		}
	})
}

// TestControlAPIScopeEnforcement asserts scope enforcement, not only command
// enforcement, for every route that names a target (review T35). The caller
// holds every command but is scoped to sandbox 1001, so a 403 can come only
// from the sandbox scope. Each row also carries an in-scope counterpart, which
// proves the denial comes from the target and not from the route refusing
// everything. Removing a resolver from any covered route fails this test.
func TestControlAPIScopeEnforcement(t *testing.T) {
	plane := newAuthzPlane(t)
	scoped := &auth.RequestIdentity{Method: "ssh-token", Token: &auth.Token{Claims: auth.TokenClaims{
		Commands: []string{"*"},
		Scope:    []string{"sandbox:1001"},
	}}}

	cases := []struct {
		name string
		// denied targets sandbox 1002; allowed targets 1001 on the same route.
		denied         authzRequest
		allowed        authzRequest
		allowedStatus  int
		allowedComment string
	}{
		{
			name:          "sandbox read by path vmid",
			denied:        authzRequest{http.MethodGet, "/v1/sandboxes/1002", ""},
			allowed:       authzRequest{http.MethodGet, "/v1/sandboxes/1001", ""},
			allowedStatus: http.StatusOK,
		},
		{
			name:          "sandbox mutation by path vmid",
			denied:        authzRequest{http.MethodPost, "/v1/sandboxes/1002/destroy", ""},
			allowed:       authzRequest{http.MethodPost, "/v1/sandboxes/1001/start", ""},
			allowedStatus: http.StatusOK, // start on a running sandbox is accepted
		},
		{
			name:          "sandbox create by body vmid",
			denied:        authzRequest{http.MethodPost, "/v1/sandboxes", `{"profile":"default","vmid":1002}`},
			allowed:       authzRequest{http.MethodPost, "/v1/sandboxes", `{"profile":"default","vmid":1001}`},
			allowedStatus: http.StatusServiceUnavailable, // no job orchestrator: past authorization
		},
		{
			name:          "sandbox validate-plan by body vmid",
			denied:        authzRequest{http.MethodPost, "/v1/sandboxes/validate-plan", `{"profile":"default","vmid":1002}`},
			allowed:       authzRequest{http.MethodPost, "/v1/sandboxes/validate-plan", `{"profile":"default","vmid":1001}`},
			allowedStatus: http.StatusOK,
		},
		{
			name:          "job read by path id",
			denied:        authzRequest{http.MethodGet, "/v1/jobs/job-1002", ""},
			allowed:       authzRequest{http.MethodGet, "/v1/jobs/job-1001", ""},
			allowedStatus: http.StatusOK,
		},
		{
			// The job body names a workspace; the job runs in that workspace's
			// sandbox, so the workspace's scope governs the request (T33).
			name:          "job create by body workspace",
			denied:        authzRequest{http.MethodPost, "/v1/jobs", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","workspace_id":"ws-1002"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/jobs", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","workspace_id":"ws-1001"}`},
			allowedStatus: http.StatusConflict, // workspace already attached: past authorization
		},
		{
			name:          "job validate-plan by body workspace",
			denied:        authzRequest{http.MethodPost, "/v1/jobs/validate-plan", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","workspace_id":"ws-1002"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/jobs/validate-plan", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","workspace_id":"ws-1001"}`},
			allowedStatus: http.StatusOK,
		},
		{
			// A session named in the body resolves through its workspace.
			name:          "job create by body session",
			denied:        authzRequest{http.MethodPost, "/v1/jobs", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","session_id":"sess-1002"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/jobs", `{"repo_url":"https://example.com/r.git","profile":"default","task":"t","session_id":"sess-1001"}`},
			allowedStatus: http.StatusConflict, // workspace already attached: past authorization
		},
		{
			name:          "workspace read by path id",
			denied:        authzRequest{http.MethodGet, "/v1/workspaces/ws-1002", ""},
			allowed:       authzRequest{http.MethodGet, "/v1/workspaces/ws-1001", ""},
			allowedStatus: http.StatusOK,
		},
		{
			name:          "workspace attach by body vmid",
			denied:        authzRequest{http.MethodPost, "/v1/workspaces/ws-1001/attach", `{"vmid":1002}`},
			allowed:       authzRequest{http.MethodPost, "/v1/workspaces/ws-1001/attach", `{"vmid":1001}`},
			allowedStatus: http.StatusOK, // re-attach to the same sandbox is accepted
		},
		{
			name:          "session read by path id",
			denied:        authzRequest{http.MethodGet, "/v1/sessions/sess-1002", ""},
			allowed:       authzRequest{http.MethodGet, "/v1/sessions/sess-1001", ""},
			allowedStatus: http.StatusOK,
		},
		{
			name:          "session create by body workspace",
			denied:        authzRequest{http.MethodPost, "/v1/sessions", `{"name":"s","profile":"default","workspace_id":"ws-1002"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/sessions", `{"name":"s","profile":"default","workspace_id":"ws-1001"}`},
			allowedStatus: http.StatusCreated,
		},
		{
			name:          "exposure create by body vmid",
			denied:        authzRequest{http.MethodPost, "/v1/exposures", `{"name":"probe-out","vmid":1002,"port":8080}`},
			allowed:       authzRequest{http.MethodPost, "/v1/exposures", `{"name":"probe-in","vmid":1001,"port":8080}`},
			allowedStatus: http.StatusServiceUnavailable, // no publisher configured: past authorization
		},
		{
			name:          "exposure delete by path name",
			denied:        authzRequest{http.MethodDelete, "/v1/exposures/exp-1002", ""},
			allowed:       authzRequest{http.MethodDelete, "/v1/exposures/exp-1001", ""},
			allowedStatus: http.StatusServiceUnavailable, // no publisher configured: past authorization
		},
		{
			// The message body scope names a workspace, so the feed it writes
			// into belongs to that workspace's sandbox (T33).
			name:          "message create by body scope",
			denied:        authzRequest{http.MethodPost, "/v1/messages", `{"scope_type":"workspace","scope_id":"ws-1002","text":"hi"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/messages", `{"scope_type":"workspace","scope_id":"ws-1001","text":"hi"}`},
			allowedStatus: http.StatusCreated,
		},
		{
			name:          "message list by query scope",
			denied:        authzRequest{http.MethodGet, "/v1/messages?scope_type=workspace&scope_id=ws-1002", ""},
			allowed:       authzRequest{http.MethodGet, "/v1/messages?scope_type=workspace&scope_id=ws-1001", ""},
			allowedStatus: http.StatusOK,
		},
		{
			name:          "message create by job scope",
			denied:        authzRequest{http.MethodPost, "/v1/messages", `{"scope_type":"job","scope_id":"job-1002","text":"hi"}`},
			allowed:       authzRequest{http.MethodPost, "/v1/messages", `{"scope_type":"job","scope_id":"job-1001","text":"hi"}`},
			allowedStatus: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, body := plane.serveAuthz(t, scoped, tc.denied.method, tc.denied.path, tc.denied.body); code != http.StatusForbidden {
				t.Errorf("out-of-scope target %s %s: got %d, want 403 (body %s)", tc.denied.method, tc.denied.path, code, body)
			}
			if code, body := plane.serveAuthz(t, scoped, tc.allowed.method, tc.allowed.path, tc.allowed.body); code != tc.allowedStatus {
				t.Errorf("in-scope target %s %s: got %d, want %d (body %s)", tc.allowed.method, tc.allowed.path, code, tc.allowedStatus, body)
			}
		})
	}
}

// authzRequest is one HTTP request in the scope-enforcement matrix.
type authzRequest struct {
	method string
	path   string
	body   string
}

// TestBodyTargetResolvers unit-tests the body-borne target resolvers added by
// the T33 audit. Each resolver must report the named target, fall back to the
// resource it names indirectly, and report 0 when the request carries no
// target or a body the handler would reject anyway.
func TestBodyTargetResolvers(t *testing.T) {
	plane := newAuthzPlane(t)

	request := func(body string) *http.Request {
		var rdr io.Reader
		if body != "" {
			rdr = bytes.NewBufferString(body)
		}
		return httptest.NewRequest(http.MethodPost, "/v1/jobs", rdr)
	}

	t.Run("job target resolves workspace and session", func(t *testing.T) {
		if got := plane.api.jobTargetScopeVMID(request(`{"workspace_id":"ws-1001"}`)); got != 1001 {
			t.Errorf("workspace_id ws-1001: got %d, want 1001", got)
		}
		if got := plane.api.jobTargetScopeVMID(request(`{"workspace_id":"ws-1002"}`)); got != 1002 {
			t.Errorf("workspace_id ws-1002: got %d, want 1002", got)
		}
		if got := plane.api.jobTargetScopeVMID(request(`{"session_id":"sess-1002"}`)); got != 1002 {
			t.Errorf("session_id sess-1002: got %d, want 1002 (resolved through its workspace)", got)
		}
		// An explicit workspace_id wins over a session_id, as in the handler.
		if got := plane.api.jobTargetScopeVMID(request(`{"workspace_id":"ws-1001","session_id":"sess-1002"}`)); got != 1001 {
			t.Errorf("workspace_id and session_id: got %d, want 1001", got)
		}
		for _, body := range []string{`{"repo_url":"x"}`, `{"workspace_create":{"name":"ws","size_gb":1}}`, `not json`, ""} {
			if got := plane.api.jobTargetScopeVMID(request(body)); got != 0 {
				t.Errorf("body %q: got %d, want 0 (no concrete target)", body, got)
			}
		}
	})

	t.Run("session create resolves its workspace", func(t *testing.T) {
		if got := plane.api.sessionCreateScopeVMID(request(`{"workspace_id":"ws-1002"}`)); got != 1002 {
			t.Errorf("workspace_id ws-1002: got %d, want 1002", got)
		}
		if got := plane.api.sessionCreateScopeVMID(request(`{"workspace_create":{"name":"ws","size_gb":1}}`)); got != 0 {
			t.Errorf("workspace_create: got %d, want 0", got)
		}
	})

	t.Run("validate-plan resolves the body vmid", func(t *testing.T) {
		if got := sandboxValidatePlanVMID(request(`{"vmid":1002}`)); got != 1002 {
			t.Errorf("vmid 1002: got %d, want 1002", got)
		}
		if got := sandboxValidatePlanVMID(request(`{"profile":"default"}`)); got != 0 {
			t.Errorf("no vmid: got %d, want 0", got)
		}
		if got := sandboxValidatePlanVMID(request(`{"vmid":0}`)); got != 0 {
			t.Errorf("vmid 0: got %d, want 0", got)
		}
	})

	t.Run("message scope resolves job, workspace, and session", func(t *testing.T) {
		ctx := context.Background()
		for _, tc := range []struct {
			scopeType, scopeID string
			want               int
		}{
			{"job", "job-1001", 1001},
			{"workspace", "ws-1002", 1002},
			{"session", "sess-1002", 1002},
			{"JOB", "job-1002", 1002},
			{"job", "missing", 0},
			{"sandbox", "1001", 0},
			{"", "ws-1001", 0},
			{"workspace", "", 0},
		} {
			if got := plane.api.messageScopeVMID(ctx, tc.scopeType, tc.scopeID); got != tc.want {
				t.Errorf("messageScopeVMID(%q, %q): got %d, want %d", tc.scopeType, tc.scopeID, got, tc.want)
			}
		}
	})

	t.Run("resolvers leave the body readable", func(t *testing.T) {
		req := request(`{"workspace_id":"ws-1001","repo_url":"https://example.com/r.git"}`)
		if got := plane.api.jobTargetScopeVMID(req); got != 1001 {
			t.Fatalf("resolver: got %d, want 1001", got)
		}
		var decoded V1JobCreateRequest
		if err := json.Unmarshal(mustReadBody(t, req), &decoded); err != nil {
			t.Fatalf("handler decode after resolver: %v", err)
		}
		if decoded.WorkspaceID == nil || *decoded.WorkspaceID != "ws-1001" {
			t.Errorf("restored body workspace_id = %v, want ws-1001", decoded.WorkspaceID)
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
