package daemon

import (
	"net/http"
	"strings"
)

func standaloneSchemaResources() []schemaResource {
	resources := []schemaResource{
		resourceSchema("/v1/sandboxes/{vmid}", methods("DELETE"), "Destroy sandbox", "V1SandboxDestroyRequest", "V1SandboxResponse", "Alias for POST /destroy; same authorization and cleanup."),
		resourceSchema("/v1/pool/status", methods("GET"), "Read configured capacity and scoped allocations", "", "V1PoolStatusResponse", "Disabled pools still return their effective configuration."),
		resourceSchema("/v1/secrets", methods("GET"), "Read redacted shared bundle", "", "V1SecretsMutationResponse", "Never returns secret credential values."),
		resourceSchema("/v1/secrets/env", methods("PUT"), "Merge environment values", "V1SecretsEnvSetRequest", "V1SecretsMutationResponse", ""),
		resourceSchema("/v1/secrets/env/{key}", methods("DELETE"), "Remove environment value", "", "V1SecretsMutationResponse", ""),
		resourceSchema("/v1/secrets/git", methods("PUT"), "Merge nonempty Git credential fields", "V1SecretsGitSetRequest", "V1SecretsMutationResponse", "Empty fields do not clear existing credentials."),
		resourceSchema("/v1/secrets/tailscale", methods("PUT"), "Merge Tailscale credentials", "V1SecretsTailscaleSetRequest", "V1SecretsMutationResponse", "Resulting bundle must contain authkey or admin_api_key, including previously stored credentials."),
		resourceSchema("/v1/secrets/tailscale", methods("DELETE"), "Clear Tailscale credentials", "", "V1SecretsMutationResponse", ""),
		resourceSchema("/v1/secrets/ssh-keys", methods("POST"), "Add shared SSH public key", "V1SecretsSSHKeyAddRequest", "V1SecretsMutationResponse", ""),
		resourceSchema("/v1/secrets/ssh-keys/{name}", methods("DELETE"), "Remove shared SSH public key", "", "V1SecretsMutationResponse", ""),
		resourceSchema("/v1/integrations", methods("GET"), "List redacted integration definitions", "", "V1IntegrationsResponse", "Requires configured integrations support."),
		resourceSchema("/v1/integrations", methods("POST"), "Create an integration", "V1IntegrationCreateRequest", "V1IntegrationResponse", "Target URL must satisfy daemon allowlist and upstream policy."),
		resourceSchema("/v1/integrations/{name}", methods("GET"), "Read redacted integration definition", "", "V1IntegrationResponse", ""),
		resourceSchema("/v1/integrations/{name}", methods("DELETE"), "Delete integration", "", "V1RegistryMutationResponse", "Requires integration.delete, not only integration.write."),
		resourceSchema("/v1/users", methods("GET"), "List users", "", "V1UsersResponse", ""),
		resourceSchema("/v1/users", methods("POST"), "Register user and SSH key", "V1UserCreateRequest", "V1UserResponse", ""),
		resourceSchema("/v1/users/{name}", methods("GET"), "Read user", "", "V1UserResponse", ""),
		resourceSchema("/v1/users/{name}", methods("DELETE"), "Remove user", "", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/users/{name}/keys", methods("POST"), "Add user SSH key", "V1UserKeyCreateRequest", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/users/{name}/keys", methods("DELETE"), "Remove user SSH key", "", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/teams", methods("GET"), "List teams", "", "V1TeamsResponse", ""),
		resourceSchema("/v1/teams", methods("POST"), "Create team", "V1TeamCreateRequest", "V1TeamResponse", ""),
		resourceSchema("/v1/teams/{name}", methods("DELETE"), "Remove team", "", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/teams/{name}/members", methods("GET"), "List team members", "", "V1TeamMembersResponse", ""),
		resourceSchema("/v1/teams/{name}/members", methods("POST"), "Add team member", "V1TeamMemberCreateRequest", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/teams/{name}/members/{user}", methods("DELETE"), "Remove team member", "", "V1RegistryMutationResponse", ""),
		resourceSchema("/v1/exec", methods("POST"), "Execute CLI argv", "ExecRequest", "ExecResponse", "Full access only. Structured args preserve argument boundaries."),
		resourceSchema("/v1/exec/dry-run", methods("POST"), "Validate CLI syntax without executing", "ExecRequest", "ExecDryRunResponse", "Syntax validity is not daemon preflight or permission to execute."),
		resourceSchema("/v1/bootstrap/fetch", methods("POST"), "Fetch guest bootstrap material", "V1BootstrapFetchRequest", "V1BootstrapFetchResponse", "Agent subnet and single-use bootstrap token; rotates sandbox secret."),
		resourceSchema("/v1/runner/report", methods("POST"), "Report guest job result", "V1RunnerReportRequest", "V1RunnerReportResponse", "Requires agent subnet and VMID-bound sandbox secret."),
		resourceSchema("/metadata/", methods("GET"), "Discover guest metadata routes", "", "MetadataIndexResponse", ""),
		resourceSchema("/metadata/identity", methods("GET"), "Read guest identity", "", "MetadataIdentityResponse", ""),
		resourceSchema("/metadata/metadata", methods("GET"), "Read guest metadata", "", "MetadataMetadataResponse", ""),
		resourceSchema("/metadata/secrets/{name}", methods("GET"), "Read an attached guest secret", "", "MetadataSecretResponse", ""),
		resourceSchema("/proxy/{name}/{path...}", methods("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"), "Forward guest integration traffic", "", "application/octet-stream", "Preserves upstream status and media type. Attachment and target policies apply; trust_agent_subnet may explicitly permit unidentified auto:all callers."),
		resourceSchema("/upload", methods("POST"), "Upload job artifact bytes", "", "V1ArtifactUploadResponse", "Requires agent subnet and unexpired job-bound artifact bearer token; daemon enforces configured size limit."),
	}
	for _, surface := range []string{"control", "bootstrap", "artifacts", "metrics"} {
		resource := resourceSchema("/healthz", methods("GET"), "Listener liveness", "", "V1HealthResponse", "Plain text ok; does not establish backend readiness.")
		resource.Surface = surface
		resources = append(resources, resource)
	}
	metric := resourceSchema("/metrics", methods("GET"), "Prometheus metrics", "", "application/octet-stream", "Prometheus exposition; served only on the configured metrics listener.")
	metric.Surface = "metrics"
	resources = append(resources, metric)
	return resources
}

func schemaConcretePath(path string) string {
	var result strings.Builder
	for {
		begin := strings.IndexByte(path, '{')
		if begin < 0 {
			result.WriteString(path)
			break
		}
		result.WriteString(path[:begin])
		path = path[begin+1:]
		end := strings.IndexByte(path, '}')
		if end < 0 {
			break
		}
		name := path[:end]
		value := "example"
		if name == "vmid" {
			value = "1001"
		}
		result.WriteString(value)
		path = path[end+1:]
	}
	return result.String()
}

func decorateSchemaResource(resource *schemaResource) {
	method := resource.Methods[0]
	if resource.Surface == "" {
		resource.Surface = "control"
	}
	resource.ErrorType = "V1ErrorResponse"
	resource.Authorization = schemaAuthorization{Authentication: "SSH-signed or legacy bearer token on network control listener; trusted local Unix socket", SandboxScope: "permission_only"}
	resource.Completion = schemaCompletion{Mode: "synchronous", Cancellation: "Disconnect or timeout does not imply rollback; inspect the resource before retrying a mutation.", Retry: "Inspect a lost mutation outcome before retrying."}
	if method == http.MethodGet {
		resource.Completion.Retry = "Repeat the read; collection cursors retain their documented ordering."
	}
	path := resource.Path
	if path == "/healthz" || resource.Surface == "metrics" {
		resource.Authorization = schemaAuthorization{Authentication: "none", SandboxScope: "listener_access"}
		resource.ResponseVariants = []schemaResponseVariant{{When: "always", Type: resource.ResponseType, Status: 200, ContentType: "text/plain"}}
		if path == "/metrics" {
			resource.ResponseVariants[0].ContentType = "text/plain; version=0.0.4"
		}
		return
	}
	switch {
	case path == "/upload":
		resource.Surface = "artifacts"
		resource.Authorization = schemaAuthorization{Authentication: "artifact bearer token and agent subnet", SandboxScope: "job_bound_token"}
	case path == "/v1/bootstrap/fetch":
		resource.Surface = "bootstrap"
		resource.Authorization = schemaAuthorization{Authentication: "single-use bootstrap token in request body and agent subnet", SandboxScope: "vmid_bound_token"}
	case path == "/v1/runner/report" || strings.HasPrefix(path, "/metadata/") || strings.HasPrefix(path, "/proxy/"):
		resource.Surface = "bootstrap"
		resource.Authorization = schemaAuthorization{Authentication: "X-AgentLab-Sandbox-Secret and agent subnet", SandboxScope: "source_sandbox"}
	case strings.HasPrefix(path, "/v1/exec"):
		resource.Authorization = schemaAuthorization{Authentication: "full-access identity (Commands [*], no sandbox scope), or trusted local/legacy caller", SandboxScope: "forbidden"}
		resource.ErrorType = "ExecErrorResponse"
	case strings.HasPrefix(path, "/v1/secrets"):
		permission := permSecretsWrite
		if method == http.MethodGet {
			permission = permSecretsRead
		}
		resource.Authorization.Permission = permission
		resource.Authorization.SandboxScope = "forbidden"
	case strings.HasPrefix(path, "/v1/integrations"):
		permission := permIntegrationWrite
		if method == http.MethodGet {
			permission = permIntegrationRead
		} else if method == http.MethodDelete {
			permission = permIntegrationDelete
		}
		resource.Authorization.Permission = permission
		resource.Authorization.SandboxScope = "forbidden"
	case strings.HasPrefix(path, "/v1/users") || strings.HasPrefix(path, "/v1/teams"):
		permission := permUserWrite
		if method == http.MethodGet {
			permission = permUserRead
		}
		resource.Authorization.Permission = permission
		resource.Authorization.SandboxScope = "forbidden"
	default:
		resource.Authorization.Permission, resource.Authorization.SandboxScope = controlSchemaPermission(path, method)
	}
	// Concrete path parameters are separately described from query selectors.
	for rest := path; strings.Contains(rest, "{"); {
		start := strings.Index(rest, "{")
		rest = rest[start+1:]
		end := strings.Index(rest, "}")
		if end < 0 {
			break
		}
		name := rest[:end]
		rest = rest[end+1:]
		value := map[string]any{"type": "string", "minLength": 1}
		if name == "vmid" {
			value = map[string]any{"type": "integer", "minimum": 1}
		}
		resource.Parameters = append(resource.Parameters, schemaParameter{Name: name, In: "path", Required: true, Schema: value})
	}
	switch path {
	case "/v1/sandboxes":
		if method == http.MethodPost {
			resource.Parameters = append(resource.Parameters, queryParameter("async", map[string]any{"type": "boolean", "default": false}, false, "Opt in to an operation response before provisioning completes."), schemaParameter{Name: "Idempotency-Key", In: "header", Schema: map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, Description: "Persisted key scoped to caller and create operation. Same payload replays; a changed payload returns 409."})
			resource.Completion = schemaCompletion{Mode: "blocking_by_default_optional_async", StatusURL: "/v1/operations/{id}", Cancellation: "Client cancellation stops observation, not accepted provisioning; daemon shutdown cancels in-flight work.", Retry: "Reuse the same idempotency key and payload; after restart inspect unknown outcomes and their VMID before allocating again."}
			resource.ResponseVariants = []schemaResponseVariant{{When: "async=true or Idempotency-Key provided", Type: "V1OperationResponse", Status: 202, ContentType: "application/json"}, {When: "blocking provisioning completes", Type: "V1SandboxResponse", Status: 201, ContentType: "application/json"}}
		}
	case "/v1/jobs":
		if method == http.MethodPost {
			resource.Completion = schemaCompletion{Mode: "background", StatusURL: "/v1/jobs/{id}", Cancellation: "Client cancellation does not cancel an accepted job.", Retry: "Job creation has no idempotency key; discover the existing job before retrying a lost outcome."}
		} else {
			resource.Parameters = append(resource.Parameters, queryParameter("after", map[string]any{"type": "string"}, false, "Exclusive job ID cursor; deterministic ascending ID ordering."), queryParameter("limit", map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}, false, "Page size; scope filtering happens before pagination."))
			for _, name := range []string{"state", "profile", "workspace"} {
				resource.Parameters = append(resource.Parameters, queryParameter(name, map[string]any{"type": "string"}, false, "Optional exact filter; state is case-insensitive."))
			}
		}
	case "/v1/jobs/{id}":
		resource.Parameters = append(resource.Parameters, queryParameter("events_tail", map[string]any{"type": "integer", "minimum": 0, "default": 50}, false, "Event count; values above 1000 are clamped."))
	case "/v1/messages":
		if method == http.MethodGet {
			resource.Parameters = append(resource.Parameters, queryParameter("scope_type", map[string]any{"type": "string", "enum": []string{"job", "workspace", "session"}}, true, "Message scope type."), queryParameter("scope_id", map[string]any{"type": "string", "minLength": 1}, true, "Message scope ID."), queryParameter("after_id", map[string]any{"type": "integer", "minimum": 0}, false, "Exclusive message cursor; after is an alias."), queryParameter("after", map[string]any{"type": "integer", "minimum": 0}, false, "Alias for after_id."), queryParameter("limit", map[string]any{"type": "integer", "minimum": 0, "default": 200}, false, "Zero uses default; values above 1000 are clamped."))
		}
	case "/v1/sandboxes/{vmid}/events":
		for _, name := range []string{"after", "tail", "limit"} {
			resource.Parameters = append(resource.Parameters, queryParameter(name, map[string]any{"type": "integer", "minimum": 0}, false, "after and tail are mutually exclusive; tail/limit above 1000 are clamped. Default limit 200."))
		}
	case "/v1/jobs/{id}/artifacts/download":
		for _, name := range []string{"name", "path"} {
			resource.Parameters = append(resource.Parameters, queryParameter(name, map[string]any{"type": "string"}, false, "Artifact selector; name and path are mutually exclusive. Omission selects latest."))
		}
	case "/v1/sandboxes/prune":
		resource.Parameters = append(resource.Parameters, queryParameter("dry_run", map[string]any{"type": "boolean", "default": false}, false, "Preview TIMEOUT records without destroying."), queryParameter("vmid", map[string]any{"type": "integer", "minimum": 1}, false, "Repeat to execute only reviewed targets."))
	case "/v1/users/{name}/keys":
		if method == http.MethodDelete {
			resource.Parameters = append(resource.Parameters, queryParameter("fingerprint", map[string]any{"type": "string", "minLength": 1}, true, "SSH key fingerprint to remove."))
		}
	}
	switch resource.RequestType {
	case "V1SandboxDestroyRequest", "V1SandboxSnapshotRestoreRequest", "V1SandboxStopAllRequest", "V1WorkspaceFSCKRequest", "V1SandboxReconcileRequest":
		resource.BodyOptional = true
	}
	if path == "/v1/sandboxes/{vmid}" && method == http.MethodDelete || strings.HasSuffix(path, "/destroy") {
		resource.Completion.Retry = "Repeated destruction is safe; cleanup only releases leases still bound to the captured work."
	}
	if path == "/v1/exec" {
		resource.ResponseVariants = []schemaResponseVariant{{When: "stream=false", Type: "ExecResponse", Status: 200, ContentType: "application/json"}, {When: "stream=true", Type: "SSEEvent", Status: 200, ContentType: "text/event-stream"}}
	}
}
func queryParameter(name string, schema map[string]any, required bool, description string) schemaParameter {
	return schemaParameter{Name: name, In: "query", Schema: schema, Required: required, Description: description}
}

func controlSchemaPermission(path, method string) (string, string) {
	switch path {
	case "/v1/jobs":
		if method == http.MethodGet {
			return permJobRead, "filter_resolved_targets"
		}
		return permJobCreate, "resolved_target"
	case "/v1/jobs/validate-plan":
		return permJobValidate, "resolved_target"
	case "/v1/sandboxes":
		if method == http.MethodGet {
			return permSandboxList, "filter_resolved_targets"
		}
		return permSandboxCreate, "resolved_target"
	case "/v1/sandboxes/inventory":
		return permSandboxList, "filter_resolved_targets"
	case "/v1/sandboxes/validate-plan":
		return permSandboxValidate, "resolved_target"
	case "/v1/sandboxes/prune", "/v1/sandboxes/stop_all", "/v1/sandboxes/reconcile":
		return permSandboxBulk, "forbidden"
	case "/v1/workspaces":
		if method == http.MethodGet {
			return permWorkspaceList, "filter_resolved_targets"
		}
		return permWorkspaceCreate, "permission_only"
	case "/v1/sessions":
		if method == http.MethodGet {
			return permSessionList, "filter_resolved_targets"
		}
		return permSessionCreate, "resolved_target"
	case "/v1/exposures":
		if method == http.MethodGet {
			return permExposureList, "filter_resolved_targets"
		}
		return permExposureCreate, "resolved_target"
	case "/v1/profiles":
		return permProfileRead, "permission_only"
	case "/v1/status":
		return permStatusRead, "permission_only"
	case "/v1/host":
		return permHostRead, "permission_only"
	case "/v1/schema":
		return permSchemaRead, "permission_only"
	case "/v1/messages":
		if method == http.MethodGet {
			return permMessageRead, "resolved_target"
		}
		return permMessageSend, "resolved_target"
	case "/v1/pool/status":
		return permPoolStatus, "filter_resolved_targets"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/v1/"), "/")
	if len(parts) > 1 {
		switch parts[0] {
		case "jobs":
			return jobActionPermission(parts[1:], method), "resolved_target"
		case "sandboxes":
			if len(parts) == 3 && parts[2] == "snapshot" {
				return permSandboxSnapshot, "resolved_target"
			}
			return sandboxActionPermission(parts[1:], method), "resolved_target"
		case "workspaces":
			return workspaceActionPermission(parts[1:], method), "resolved_target"
		case "sessions":
			return sessionActionPermission(parts[1:], method), "resolved_target"
		case "exposures":
			return permExposureDelete, "resolved_target"
		case "operations":
			return permSandboxCreate, "caller_and_resolved_target"
		}
	}
	return "", "permission_only"
}

func schemaProbeRequest(resource *schemaResource) *http.Request {
	request, _ := http.NewRequest(resource.Methods[0], "http://schema.local"+schemaConcretePath(resource.Path), nil)
	return request
}

// WithSchemaSurface binds a configured listener's mux for availability discovery.
// Call during construction, before serving requests.
func (api *ControlAPI) WithSchemaSurface(name string, mux *http.ServeMux) *ControlAPI {
	if api.schemaSurfaces == nil {
		api.schemaSurfaces = make(map[string]*http.ServeMux)
	}
	api.schemaSurfaces[name] = mux
	return api
}
func configuredSchemaRoutes(resources []schemaResource) map[string]bool {
	routes := make(map[string]bool)
	for _, resource := range resources {
		for _, method := range resource.Methods {
			routes[resource.Surface+" "+method+" "+resource.Path] = resource.Available != nil && *resource.Available
		}
	}
	return routes
}
func (api *ControlAPI) schemaBackendCapabilities() map[string]bool {
	available := api.backend != nil
	capabilities := map[string]bool{"snapshots": available, "suspend": available, "workspace_volumes": available && api.workspaceMgr != nil, "vm_configuration": available}
	if adapter, ok := api.backend.(*sandboxBackendAdapter); ok {
		declared := adapter.inner.Capabilities()
		capabilities["snapshots"] = declared.Snapshots
		capabilities["suspend"] = declared.Suspend
		// The adapter does not implement controller-managed Proxmox volume APIs,
		// even if the backend itself can mount a container volume during creation.
		capabilities["workspace_volumes"] = false
		capabilities["vm_configuration"] = false
	}
	return capabilities
}
