package daemon

import (
	"context"
	"net/http"
	"strings"

	"github.com/agentlab/agentlab/internal/auth"
)

// Network authorization for the control API (review C1).
//
// The local Unix socket has no auth middleware and carries no identity, so it
// remains a trusted full-access path. The TCP control listener authenticates
// callers via auth.WrapNetwork and, for SSH-signed tokens, now also authorizes
// them: each handler calls ControlAPI.authorize with a typed permission and,
// where a concrete sandbox is targeted, a resolver that yields its VMID.
//
// Permissions are dotted names matched against a token's Commands claim via
// auth.Token.IsCommandAllowed (exact or dot-boundary namespace). Granting the
// bare namespace "sandbox" enables every sandbox.* permission, "job" every
// job.* permission, and so on. A token carrying Commands=["*"] (full access)
// and an empty Scope is unconstrained; the same is true of the legacy bearer
// token, whose identity has Token==nil and is short-circuited here.
//
// Sandbox scope is enforced only for tokens that declare one, and only against
// the concrete VMID a request targets. Collection, create, and bulk routes have
// no single target VMID, so they are governed by command permission alone (bulk
// routes additionally reject any sandbox-scoped token, since they are
// inherently cross-sandbox). List routes additionally filter their responses.
const (
	permSandboxList            = "sandbox.list"
	permSandboxRead            = "sandbox.read"
	permSandboxCreate          = "sandbox.create"
	permSandboxStart           = "sandbox.start"
	permSandboxStop            = "sandbox.stop"
	permSandboxPause           = "sandbox.pause"
	permSandboxResume          = "sandbox.resume"
	permSandboxUpdate          = "sandbox.update"
	permSandboxTouch           = "sandbox.touch"
	permSandboxRevert          = "sandbox.revert"
	permSandboxDestroy         = "sandbox.destroy"
	permSandboxSnapshot        = "sandbox.snapshot"
	permSandboxSnapshotRestore = "sandbox.snapshot.restore"
	permSandboxLease           = "sandbox.lease"
	permSandboxEvents          = "sandbox.events"
	permSandboxDoctor          = "sandbox.doctor"
	permSandboxValidate        = "sandbox.validate"
	permSandboxBulk            = "sandbox.bulk"

	permJobCreate    = "job.create"
	permJobRead      = "job.read"
	permJobArtifacts = "job.artifacts"
	permJobDoctor    = "job.doctor"
	permJobValidate  = "job.validate"

	permWorkspaceList            = "workspace.list"
	permWorkspaceRead            = "workspace.read"
	permWorkspaceCreate          = "workspace.create"
	permWorkspaceCheck           = "workspace.check"
	permWorkspaceFSCK            = "workspace.fsck"
	permWorkspaceSnapshot        = "workspace.snapshot"
	permWorkspaceSnapshotRestore = "workspace.snapshot.restore"
	permWorkspaceAttach          = "workspace.attach"
	permWorkspaceDetach          = "workspace.detach"
	permWorkspaceRebind          = "workspace.rebind"
	permWorkspaceFork            = "workspace.fork"
	permWorkspaceLease           = "workspace.lease"

	permSessionList   = "session.list"
	permSessionRead   = "session.read"
	permSessionCreate = "session.create"
	permSessionResume = "session.resume"
	permSessionStop   = "session.stop"
	permSessionFork   = "session.fork"
	permSessionDoctor = "session.doctor"

	permExposureList   = "exposure.list"
	permExposureCreate = "exposure.create"
	permExposureDelete = "exposure.delete"

	permProfileRead = "profile.read"
	permSchemaRead  = "schema.read"
	permStatusRead  = "status.read"
	permHostRead    = "host.read"
	permMessageRead = "message.read"
	permMessageSend = "message.create"

	// The secrets bundle, the integration registry, and the user registry are
	// global resources: they are not bound to one sandbox, so any token that
	// declares a sandbox scope is refused for them outright.
	permSecretsRead      = "secrets.read"
	permSecretsWrite     = "secrets.write"
	permIntegrationRead  = "integration.read"
	permIntegrationWrite = "integration.write"
	permUserRead         = "user.read"
	permUserWrite        = "user.write"

	// integration.delete is a grant above bare integration.write: deleting a
	// live name and recreating it (which integration.write alone covers)
	// silently redirects every sandbox that resolves that name, so removal
	// must be elevated on its own (review F11).
	permIntegrationDelete = "integration.delete"

	// Pool status carries no secrets; scoped tokens may read it, and the
	// allocations list is filtered to their scope (PoolAPI.handlePoolStatus).
	permPoolStatus = "pool.status"
)

// authorize enforces command and sandbox-scope authorization for a request.
// It returns true when the caller may proceed; otherwise it has written a 403
// response and the handler must return immediately.
//
// Trusted callers bypass enforcement: a nil identity (the local Unix socket,
// which carries no auth middleware) and identities whose Token is nil (the
// legacy bearer token). Only SSH-signed scoped tokens are constrained.
//
// resolve returns the sandbox VMID the request targets. It is invoked ONLY when
// the caller carries a sandbox-scoped token, so the (possibly database-backed)
// resolution never runs for full-access callers. Returning 0 means the request
// has no concrete sandbox target (collection, create, or unresolvable), in
// which case the sandbox scope is not consulted.
//
// bulk, when true, marks the operation as inherently cross-sandbox
// (stop_all/prune/reconcile): any sandbox-scoped token is denied outright
// because scope cannot meaningfully bound a global mutation.
func (api *ControlAPI) authorize(w http.ResponseWriter, r *http.Request, perm string, resolve func() int, bulk bool) bool {
	if !authorizeChecked(w, r, perm, bulk) {
		return false
	}
	if resolve == nil {
		return true
	}
	id := auth.FromContext(r.Context())
	if id == nil || id.Token == nil || len(id.Token.Claims.Scope) == 0 {
		return true
	}
	if vmid := resolve(); vmid > 0 && !id.IsSandboxAllowed(vmid) {
		writeAuthzDenied(w, perm)
		return false
	}
	return true
}

// authorizeChecked is the command and global-scope enforcement core shared by
// ControlAPI.authorize and authorizeStandalone. It rejects sandbox-scoped
// tokens for cross-sandbox operations (the bulk/global rule) and any token
// that lacks perm. Trusted callers (nil identity, legacy bearer token) pass.
func authorizeChecked(w http.ResponseWriter, r *http.Request, perm string, global bool) bool {
	id := auth.FromContext(r.Context())
	if id == nil || id.Token == nil {
		return true
	}
	if global && len(id.Token.Claims.Scope) > 0 {
		writeAuthzDenied(w, perm)
		return false
	}
	if !id.IsCommandAllowed(perm) {
		writeAuthzDenied(w, perm)
		return false
	}
	return true
}

// authorizeStandalone enforces authorization for API handlers registered on
// the control mux outside ControlAPI (secrets, integrations, users, pool). It
// mirrors authorize with a nil resolver: global marks the resource as
// cross-sandbox, so any sandbox-scoped token is denied outright. Non-global
// resources stay readable to scoped tokens, which then filter their responses
// with sandboxScopeFilter.
func authorizeStandalone(w http.ResponseWriter, r *http.Request, perm string, global bool) bool {
	return authorizeChecked(w, r, perm, global)
}

// writeAuthzDenied writes a uniform 403 for an authorization failure. It avoids
// echoing the token or scope details.
func writeAuthzDenied(w http.ResponseWriter, perm string) {
	writeError(w, http.StatusForbidden, "token is not authorized for "+perm)
}

// sandboxScopeFilter returns nil when the caller may see every sandbox (trusted
// path, legacy token, or an unscoped SSH token), otherwise a predicate that
// reports whether a given VMID falls within the caller's declared scope. List
// handlers use it to filter responses for scoped tokens.
func sandboxScopeFilter(r *http.Request) func(int) bool {
	id := auth.FromContext(r.Context())
	if id == nil || id.Token == nil || len(id.Token.Claims.Scope) == 0 {
		return nil
	}
	return func(vmid int) bool { return id.IsSandboxAllowed(vmid) }
}

// --- resource → sandbox VMID resolvers (DB-backed; run only for scoped tokens) ---

func (api *ControlAPI) jobSandboxVMID(ctx context.Context, jobID string) int {
	job, err := api.store.GetJob(ctx, jobID)
	if err != nil || job.SandboxVMID == nil {
		return 0
	}
	return *job.SandboxVMID
}

// workspaceSandboxVMID resolves the sandbox a workspace belongs to: its
// current attachment, or when detached, the sandbox that detached it. A
// detached workspace therefore stays governed by the scope it came from
// (review F5). A never-attached workspace resolves to 0 and stays governed by
// command permission alone.
func (api *ControlAPI) workspaceSandboxVMID(ctx context.Context, id string) int {
	ws, err := api.store.GetWorkspace(ctx, id)
	if err != nil {
		return 0
	}
	if ws.AttachedVM != nil {
		return *ws.AttachedVM
	}
	if ws.LastAttachedVM != nil {
		return *ws.LastAttachedVM
	}
	return 0
}

func (api *ControlAPI) sessionSandboxVMID(ctx context.Context, id string) int {
	s, err := api.store.GetSession(ctx, id)
	if err != nil || s.CurrentVMID == nil {
		return 0
	}
	return *s.CurrentVMID
}

func (api *ControlAPI) exposureSandboxVMID(ctx context.Context, name string) int {
	e, err := api.store.GetExposure(ctx, name)
	if err != nil {
		return 0
	}
	return e.VMID
}

// --- permission derivation: (url parts, method) → permission ---
//
// These mirror the per-handler dispatch tables below. An unmapped action
// returns "", which leaves authorization to the handler's own 404/405 — it can
// never widen access, only fail closed if a future action is added without a
// mapping. The deny-by-default test matrix (api_authz_test.go) guards drift.

// sandboxActionPermission maps a /v1/sandboxes/{id}/... request to its
// permission. parts is the path tail split on '/' (parts[0] is the vmid).
func sandboxActionPermission(parts []string, method string) string {
	switch len(parts) {
	case 1:
		if method == http.MethodGet {
			return permSandboxRead
		}
	case 2:
		return sandboxSubPermission(parts[1], method)
	case 3:
		if parts[1] == "lease" && parts[2] == "renew" && method == http.MethodPost {
			return permSandboxLease
		}
	case 4:
		if parts[1] == "snapshots" && parts[3] == "restore" && method == http.MethodPost {
			return permSandboxSnapshotRestore
		}
	}
	return ""
}

func sandboxSubPermission(action, method string) string {
	switch action {
	case "start", "stop", "pause", "resume", "update", "touch", "revert", "destroy":
		if method == http.MethodPost {
			return "sandbox." + action
		}
	case "snapshots":
		if method == http.MethodGet || method == http.MethodPost {
			return permSandboxSnapshot
		}
	case "events":
		if method == http.MethodGet {
			return permSandboxEvents
		}
	case "doctor":
		if method == http.MethodPost {
			return permSandboxDoctor
		}
	}
	return ""
}

// jobActionPermission maps a /v1/jobs/{id}/... request to its permission.
func jobActionPermission(parts []string, method string) string {
	switch len(parts) {
	case 1:
		if method == http.MethodGet {
			return permJobRead
		}
	case 2:
		switch parts[1] {
		case "artifacts":
			if method == http.MethodGet {
				return permJobArtifacts
			}
		case "doctor":
			if method == http.MethodPost {
				return permJobDoctor
			}
		}
	case 3:
		if parts[1] == "artifacts" && parts[2] == "download" && method == http.MethodGet {
			return permJobArtifacts
		}
	}
	return ""
}

// workspaceActionPermission maps a /v1/workspaces/{id}/... request to its
// permission.
func workspaceActionPermission(parts []string, method string) string {
	switch len(parts) {
	case 1:
		if method == http.MethodGet {
			return permWorkspaceRead
		}
	case 2:
		switch parts[1] {
		case "check":
			if method == http.MethodGet {
				return permWorkspaceCheck
			}
		case "fsck":
			if method == http.MethodPost {
				return permWorkspaceFSCK
			}
		case "snapshots":
			if method == http.MethodGet || method == http.MethodPost {
				return permWorkspaceSnapshot
			}
		case "attach":
			if method == http.MethodPost {
				return permWorkspaceAttach
			}
		case "detach":
			if method == http.MethodPost {
				return permWorkspaceDetach
			}
		case "rebind":
			if method == http.MethodPost {
				return permWorkspaceRebind
			}
		case "fork":
			if method == http.MethodPost {
				return permWorkspaceFork
			}
		}
	case 3:
		if parts[1] == "lease" && parts[2] == "clear" && method == http.MethodPost {
			return permWorkspaceLease
		}
	case 4:
		if parts[1] == "snapshots" && parts[3] == "restore" && method == http.MethodPost {
			return permWorkspaceSnapshotRestore
		}
	}
	return ""
}

// sessionActionPermission maps a /v1/sessions/{id}/... request to its
// permission.
func sessionActionPermission(parts []string, method string) string {
	if len(parts) != 2 {
		if len(parts) == 1 && method == http.MethodGet {
			return permSessionRead
		}
		return ""
	}
	if method != http.MethodPost {
		return ""
	}
	switch parts[1] {
	case "resume":
		return permSessionResume
	case "stop":
		return permSessionStop
	case "fork":
		return permSessionFork
	case "doctor":
		return permSessionDoctor
	}
	return ""
}

// pathTail splits the part of r.URL.Path after prefix into '/'-separated
// non-empty segments. It returns nil when there is no tail.
func pathTail(r *http.Request, prefix string) []string {
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}
