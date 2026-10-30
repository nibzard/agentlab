package daemon

import (
	"net/http"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/buildinfo"
	"github.com/agentlab/agentlab/internal/hostidentity"
	"github.com/agentlab/agentlab/internal/protocol"
	"github.com/agentlab/agentlab/internal/proxmox"
)

func (api *ControlAPI) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	if !api.authorize(w, r, permSchemaRead, nil, false) {
		return
	}
	identity, _ := hostidentity.Local()
	nativeWorkspaceStorage := false
	nativeWorkspaceRecovery := false
	if api.workspaceMgr != nil {
		if capability, ok := api.workspaceMgr.backend.(interface{ WorkspaceStorageSupported() bool }); ok {
			nativeWorkspaceStorage = capability.WorkspaceStorageSupported()
		}
	}

	if nativeWorkspaceStorage && api.workspaceMgr != nil {
		_, nativeWorkspaceRecovery = api.workspaceMgr.backend.(proxmox.WorkspaceCopyRecoveryBackend)
	}

	access := protocol.Authorization{Method: "local_trusted", Commands: []string{"*"}, Scope: []string{}}
	if actor := auth.FromContext(r.Context()); actor != nil {
		access.Method = actor.Method
		access.Issuer = actor.Fingerprint
		access.Subject = actor.Subject
		if actor.Token != nil {
			access.Commands = append([]string{}, actor.Token.Claims.Commands...)
			access.Scope = append([]string{}, actor.Token.Claims.Scope...)
			access.ExpiresAt = actor.Token.Claims.ExpiresAt
		}
	}
	writeJSON(w, http.StatusOK, protocol.Capabilities{Version: buildinfo.Version, ProtocolVersion: 1, APISchemaVersion: controlAPISchemaVersion, EventSchemaVersion: eventContractSchemaVersion, ServerIdentity: identity, Authorization: access, Features: map[string]bool{
		"request_server_identity": identity != "",
		"workspace_snapshots":     nativeWorkspaceStorage, "workspace_forks": nativeWorkspaceStorage, "workspace_fork_observation": true, "workspace_fork_recovery": nativeWorkspaceRecovery,
		"job_cancellation": true, "session_job_cancellation": false,
		"execution_inspection":       api.artifactRoot != "",
		"repository_result_recovery": api.artifactRoot != "",
		"repository_result_cleanup":  api.artifactRoot != "",
		"jobs":                       true, "standalone_sandboxes": true, "scoped_tokens": true, "server_identity": identity != "",
		"idempotent_job_submission": identity != "", "idempotent_workspace_create": identity != "" && api.workspaceMgr != nil, "idempotent_branch_sessions": false, "repository_upload": api.artifactRoot != "", "repository_snapshot_jobs": api.artifactRoot != "", "repository_results": api.artifactRoot != "", "job_test_commands": true, "repository_transfer": false, "token_revocation": identity != "" && api.store != nil, "token_renewal": identity != "" && api.store != nil, "legacy_token_revocation": false, "tailcat": false,
	}})
}
