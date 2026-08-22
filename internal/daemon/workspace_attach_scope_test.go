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
	"github.com/agentlab/agentlab/internal/models"
)

// setupWorkspaceAttachScope builds a control API with two sandboxes and three
// workspaces: one attached to 1001, one detached after an attachment to 1002
// (the foreign detached workspace), and one never attached (the shared pool).
func setupWorkspaceAttachScope(t *testing.T) (*ControlAPI, *WorkspaceManager, *http.ServeMux) {
	t.Helper()
	store := newTestStore(t)
	backend := &stubBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	workspaceMgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))
	api := NewControlAPI(store, map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}, manager, workspaceMgr, nil, "", log.New(io.Discard, "", 0)).WithBackend(backend)

	ctx := context.Background()
	now := time.Now().UTC()
	for _, vmid := range []int{1001, 1002, 1003} {
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
	for _, ws := range []struct {
		id   string
		name string
	}{
		{"ws-attached", "attached"},
		{"ws-foreign-detached", "foreign-detached"},
		{"ws-fresh", "fresh"},
	} {
		if err := store.CreateWorkspace(ctx, models.Workspace{
			ID:          ws.id,
			Name:        ws.name,
			Storage:     "local",
			VolumeID:    "vol-" + ws.id,
			SizeGB:      10,
			CreatedAt:   now,
			LastUpdated: now,
		}); err != nil {
			t.Fatalf("create workspace %s: %v", ws.id, err)
		}
	}

	// ws-attached: currently attached to 1001.
	if _, err := workspaceMgr.Attach(ctx, "ws-attached", 1001); err != nil {
		t.Fatalf("attach ws-attached: %v", err)
	}
	// ws-foreign-detached: attached to 1002, then detached. Its last
	// attachment is 1002, so a token scoped to 1001 must not attach it.
	if _, err := workspaceMgr.Attach(ctx, "ws-foreign-detached", 1002); err != nil {
		t.Fatalf("attach ws-foreign-detached: %v", err)
	}
	if _, err := workspaceMgr.Detach(ctx, "ws-foreign-detached"); err != nil {
		t.Fatalf("detach ws-foreign-detached: %v", err)
	}

	mux := http.NewServeMux()
	api.Register(mux)
	return api, workspaceMgr, mux
}

// scopedAttachIdentity returns a token that may attach workspaces but only
// inside the given sandbox scope.
func scopedAttachIdentity(scope string) *auth.RequestIdentity {
	return &auth.RequestIdentity{
		Method: "ssh-token",
		Token: &auth.Token{
			Claims: auth.TokenClaims{
				Commands: []string{"workspace", "workspace.list"},
				Scope:    []string{scope},
			},
		},
	}
}

func serveWorkspaceAttach(t *testing.T, mux *http.ServeMux, id *auth.RequestIdentity, method, path, body string) (int, []byte) {
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

// TestWorkspaceAttachTargetScopeDenied covers review F5, task T13: the vmid in
// the attach body is scope-checked, so a token scoped to one sandbox cannot
// attach a workspace into another sandbox.
func TestWorkspaceAttachTargetScopeDenied(t *testing.T) {
	_, _, mux := setupWorkspaceAttachScope(t)
	scoped := scopedAttachIdentity("sandbox:1002")

	// The fresh workspace belongs to the shared pool, but the target vmid
	// 1001 is outside the token scope.
	code, _ := serveWorkspaceAttach(t, mux, scoped, http.MethodPost, "/v1/workspaces/ws-fresh/attach", `{"vmid":1001}`)
	if code != http.StatusForbidden {
		t.Fatalf("attach into out-of-scope vmid 1001: got %d, want 403", code)
	}

	// The same request inside the scope succeeds.
	code, body := serveWorkspaceAttach(t, mux, scoped, http.MethodPost, "/v1/workspaces/ws-fresh/attach", `{"vmid":1002}`)
	if code != http.StatusOK {
		t.Fatalf("attach into in-scope vmid 1002: got %d, want 200 (body %s)", code, body)
	}

	// A trusted caller (no identity, the local socket) stays unconstrained.
	code, _ = serveWorkspaceAttach(t, mux, nil, http.MethodPost, "/v1/workspaces/ws-foreign-detached/attach", `{"vmid":1003}`)
	if code != http.StatusOK {
		t.Fatalf("trusted attach into vmid 1003: got %d, want 200", code)
	}
}

// TestWorkspaceAttachForeignDetachedDenied covers review F5, task T14: a
// detached workspace keeps the vmid of its last attachment, and a scoped token
// outside that scope cannot attach it.
func TestWorkspaceAttachForeignDetachedDenied(t *testing.T) {
	_, _, mux := setupWorkspaceAttachScope(t)

	// The workspace was last attached to 1002. A token scoped to 1001 is
	// refused even though the target vmid 1001 is in its scope.
	code, _ := serveWorkspaceAttach(t, mux, scopedAttachIdentity("sandbox:1001"), http.MethodPost, "/v1/workspaces/ws-foreign-detached/attach", `{"vmid":1001}`)
	if code != http.StatusForbidden {
		t.Fatalf("attach foreign detached workspace: got %d, want 403", code)
	}

	// A token scoped to the last attachment (1002) may reattach it, into its
	// own scope.
	code, body := serveWorkspaceAttach(t, mux, scopedAttachIdentity("sandbox:1002"), http.MethodPost, "/v1/workspaces/ws-foreign-detached/attach", `{"vmid":1002}`)
	if code != http.StatusOK {
		t.Fatalf("reattach by owning scope: got %d, want 200 (body %s)", code, body)
	}
}

// TestWorkspaceListFiltersDetachedByLastAttachment covers the F5 list leak: a
// scoped token must not see detached workspaces whose last attachment is
// outside its scope. A never-attached workspace stays visible as part of the
// shared pool.
func TestWorkspaceListFiltersDetachedByLastAttachment(t *testing.T) {
	_, _, mux := setupWorkspaceAttachScope(t)

	code, body := serveWorkspaceAttach(t, mux, scopedAttachIdentity("sandbox:1001"), http.MethodGet, "/v1/workspaces", "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/workspaces: got %d, want 200", code)
	}
	var resp V1WorkspacesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode workspaces: %v", err)
	}
	seen := map[string]bool{}
	for _, ws := range resp.Workspaces {
		seen[ws.ID] = true
	}
	if !seen["ws-attached"] {
		t.Errorf("scoped list missing in-scope attached workspace: %s", body)
	}
	if !seen["ws-fresh"] {
		t.Errorf("scoped list missing never-attached shared workspace: %s", body)
	}
	if seen["ws-foreign-detached"] {
		t.Errorf("scoped list leaked foreign detached workspace: %s", body)
	}
}

// TestWorkspaceLastAttachedSurvivesDetach checks the store round-trip for the
// last-attachment column: attach records it, detach keeps it, and a fresh
// workspace starts without one.
func TestWorkspaceLastAttachedSurvivesDetach(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &stubBackend{}
	mgr := NewWorkspaceManager(store, backend, log.New(io.Discard, "", 0))

	now := time.Now().UTC()
	if err := store.CreateSandbox(ctx, models.Sandbox{
		VMID:          1001,
		Name:          "sb",
		Profile:       "default",
		State:         models.SandboxRunning,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if err := store.CreateWorkspace(ctx, models.Workspace{
		ID:          "ws-1",
		Name:        "ws",
		Storage:     "local",
		VolumeID:    "vol-ws-1",
		SizeGB:      10,
		CreatedAt:   now,
		LastUpdated: now,
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	fresh, err := store.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if fresh.LastAttachedVM != nil {
		t.Fatalf("fresh workspace has last attachment %d, want none", *fresh.LastAttachedVM)
	}

	if _, err := mgr.Attach(ctx, "ws-1", 1001); err != nil {
		t.Fatalf("attach: %v", err)
	}
	attached, err := store.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if attached.LastAttachedVM == nil || *attached.LastAttachedVM != 1001 {
		t.Fatalf("attached workspace last attachment = %+v, want 1001", attached.LastAttachedVM)
	}

	if _, err := mgr.Detach(ctx, "ws-1"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	detached, err := store.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if detached.AttachedVM != nil {
		t.Fatalf("detached workspace still attached to %d", *detached.AttachedVM)
	}
	if detached.LastAttachedVM == nil || *detached.LastAttachedVM != 1001 {
		t.Fatalf("detached workspace last attachment = %+v, want 1001", detached.LastAttachedVM)
	}
}
