package daemon

// request rules describe semantic requirements that JSON wire tags cannot infer.
// Runtime preflight still resolves configured profiles, storage, leases and policy.
type schemaRequestRule struct {
	Required    []string
	Example     map[string]any
	Description string
}

func schemaRequestRules() map[string]schemaRequestRule {
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA example"
	return map[string]schemaRequestRule{
		"V1BootstrapFetchRequest":          {[]string{"token", "vmid"}, map[string]any{"token": "issued-bootstrap-token", "vmid": 1001}, "Single-use bootstrap token must belong to the VMID and be unexpired."},
		"V1SecretsEnvSetRequest":           {[]string{"env"}, map[string]any{"env": map[string]any{"EXAMPLE_MODE": "test"}}, "Merge environment entries into the shared encrypted bundle."},
		"V1SecretsGitSetRequest":           {nil, map[string]any{"username": "git"}, "Only nonempty fields are merged; empty values do not clear stored credentials."},
		"V1SecretsTailscaleSetRequest":     {nil, map[string]any{"authkey": "example-not-a-real-authkey"}, "Merged bundle must contain authkey or admin_api_key, including credentials already stored."},
		"V1SecretsSSHKeyAddRequest":        {[]string{"name", "key"}, map[string]any{"name": "example", "key": key}, "key is an OpenSSH authorized public key, not a private key."},
		"V1JobCreateRequest":               {[]string{"repo_url", "profile", "task"}, map[string]any{"repo_url": "https://example.com/repo.git", "profile": "default", "task": "run tests"}, "ref defaults to main and mode to dangerous. Profile behavior supplies omitted TTL/keepalive. Workspace and session identifiers must resolve and agree."},
		"V1JobValidatePlanRequest":         {nil, map[string]any{"repo_url": "https://example.com/repo.git", "profile": "default", "task": "run tests"}, "Accepts incomplete plans and returns ok:false with structured findings; performs no allocation."},
		"V1SandboxCreateRequest":           {[]string{"profile"}, map[string]any{"profile": "default", "name": "example", "ttl_minutes": 60}, "Profile must exist and support provisioning. Name defaults to sandbox-<vmid>; type/image inherit the profile. Omitted TTL/keepalive inherit profile behavior. Async and keyed creation return an operation."},
		"V1SandboxValidatePlanRequest":     {nil, map[string]any{"profile": "default"}, "Accepts incomplete plans and returns ok:false findings without allocation."},
		"V1SandboxDestroyRequest":          {nil, map[string]any{}, "force is a compatibility alias; both paths stop, detach, destroy and release owned leases."},
		"V1SandboxRevertRequest":           {nil, map[string]any{"restart": false}, "Omitted restart restarts only if previously running; force permits reverting active work subject to backend guards."},
		"V1SandboxUpdateRequest":           {nil, map[string]any{"cores": 2}, "At least one non-null cores, memory_mb or tags is required. Tags replace the set; [] clears it."},
		"V1SandboxSnapshotCreateRequest":   {[]string{"name"}, map[string]any{"name": "checkpoint"}, "Snapshot backend and detached-workspace guards apply; force permits active sandbox state."},
		"V1SandboxSnapshotRestoreRequest":  {nil, map[string]any{}, "Restores the snapshot named by the path."},
		"V1SandboxReconcileRequest":        {nil, map[string]any{"apply": false}, "apply defaults to false; false reports drift without mutations."},
		"V1SandboxStopAllRequest":          {nil, map[string]any{"force": false}, "Returns per-sandbox results; requires an unscoped sandbox.bulk grant."},
		"V1LeaseRenewRequest":              {[]string{"ttl_minutes"}, map[string]any{"ttl_minutes": 60}, "Renewal requires a RUNNING sandbox."},
		"V1WorkspaceCreateRequest":         {[]string{"name", "size_gb"}, map[string]any{"name": "example-workspace", "size_gb": 10}, "Storage defaults to the workspace manager configuration."},
		"V1WorkspaceForkRequest":           {[]string{"name"}, map[string]any{"name": "forked-workspace"}, "Optional from_snapshot selects a stored snapshot; volume must support cloning."},
		"V1WorkspaceAttachRequest":         {[]string{"vmid"}, map[string]any{"vmid": 1001}, "Workspace must be detached and not leased to unrelated work."},
		"V1WorkspaceRebindRequest":         {[]string{"profile"}, map[string]any{"profile": "default", "keep_old": false}, "Provisions a replacement sandbox; keep_old defaults to false."},
		"V1WorkspaceSnapshotCreateRequest": {[]string{"name"}, map[string]any{"name": "checkpoint"}, "Workspace must be detached, unleased and use snapshot-capable storage."},
		"V1WorkspaceFSCKRequest":           {nil, map[string]any{"repair": false}, "repair defaults to false; repair requires a detached, unleased volume."},
		"V1SessionCreateRequest":           {[]string{"name", "profile"}, map[string]any{"name": "example-session", "profile": "default", "workspace_create": map[string]any{"name": "session-workspace", "size_gb": 10}}, "Exactly one non-null workspace_id or workspace_create is required."},
		"V1SessionForkRequest":             {[]string{"name"}, map[string]any{"name": "forked-session", "workspace_create": map[string]any{"name": "fork-workspace", "size_gb": 10}}, "Exactly one workspace choice; omitted profile inherits the origin session profile."},
		"V1ExposureCreateRequest":          {[]string{"vmid", "port"}, map[string]any{"vmid": 1001, "port": 8080}, "Target IP is resolved from the sandbox; callers cannot supply target_ip. Empty name is derived by the publisher."},
		"V1MessageCreateRequest":           {[]string{"scope_type", "scope_id"}, map[string]any{"scope_type": "job", "scope_id": "job-example", "text": "example message"}, "Scope is job, workspace or session and is independently authorized."},
		"V1RunnerReportRequest":            {[]string{"job_id", "vmid", "status"}, map[string]any{"job_id": "job-example", "vmid": 1001, "status": "COMPLETED"}, "Status accepts RUNNING, COMPLETED, FAILED or TIMEOUT, case-insensitively. Sandbox secret must match vmid."},
		"V1IntegrationCreateRequest":       {[]string{"name"}, map[string]any{"name": "example-api", "target": "https://example.com/api", "secret": "example-not-a-real-token", "attach": "auto:all"}, "type defaults to http-proxy, secret_type to bearer, attach to auto:all. HTTP/LLM types require a safe target; non-LLM types require secret. LLM provider is openai, anthropic or ollama."},
		"V1UserCreateRequest":              {[]string{"name", "key"}, map[string]any{"name": "example-user", "key": key, "role": "user"}, "key is an SSH public key. Role admin grants admin; all other values select user."},
		"V1UserKeyCreateRequest":           {[]string{"key"}, map[string]any{"key": key}, "key is an SSH authorized public key."},
		"V1TeamCreateRequest":              {[]string{"name"}, map[string]any{"name": "example-team", "description": "Example team"}, "Team name must be unique."},
		"V1TeamMemberCreateRequest":        {[]string{"user_id"}, map[string]any{"user_id": "existing-user-id", "role": "user"}, "User and team must exist; admin selects admin, other role values select user."},
		"ExecRequest":                      {nil, map[string]any{"args": []any{"sandbox", "list", "--json"}, "timeout": 30}, "Nonempty args takes precedence over quoted command. Timeout <=0 selects 300s; values above 3600s are clamped. Exec is full-access only; dry-run reports syntax validation without executing."},
	}
}

func applySchemaRequestRules(defs map[string]any) {
	for name, rule := range schemaRequestRules() {
		definition, ok := defs[name].(map[string]any)
		if !ok {
			continue
		}
		if len(rule.Required) > 0 {
			definition["required"] = rule.Required
		}
		definition["description"] = rule.Description
		definition["examples"] = []any{rule.Example}
		properties := definition["properties"].(map[string]any)
		for _, key := range rule.Required {
			if property, ok := properties[key].(map[string]any); ok && property["type"] == "string" {
				property["minLength"] = 1
			}
		}
	}
	set := func(name, key string, schema map[string]any) {
		if definition, ok := defs[name].(map[string]any); ok {
			definition["properties"].(map[string]any)[key] = schema
		}
	}
	positive := func(nullable bool) map[string]any {
		schema := map[string]any{"type": "integer", "minimum": 1}
		if nullable {
			schema["type"] = []string{"integer", "null"}
		}
		return schema
	}
	for _, name := range []string{"V1SandboxCreateRequest", "V1JobCreateRequest", "V1WorkspaceRebindRequest"} {
		set(name, "ttl_minutes", positive(true))
	}
	set("V1SandboxCreateRequest", "vmid", positive(true))
	for _, name := range []string{"V1WorkspaceAttachRequest", "V1ExposureCreateRequest", "V1RunnerReportRequest"} {
		set(name, "vmid", positive(false))
	}
	set("V1LeaseRenewRequest", "ttl_minutes", positive(false))
	set("V1WorkspaceCreateRequest", "size_gb", positive(false))
	set("V1ExposureCreateRequest", "port", map[string]any{"type": "integer", "minimum": 1, "maximum": 65535})
	set("V1JobCreateRequest", "workspace_wait_seconds", map[string]any{"type": []string{"integer", "null"}, "minimum": 0})
	set("V1JobCreateRequest", "ref", map[string]any{"type": "string", "default": "main"})
	set("V1JobCreateRequest", "mode", map[string]any{"type": "string", "default": "dangerous"})
	set("V1MessageCreateRequest", "scope_type", map[string]any{"type": "string", "enum": []string{"job", "workspace", "session"}})
	set("V1SandboxCreateRequest", "type", map[string]any{"type": "string", "enum": []string{"", "vm", "lxc"}, "description": "Empty/omitted inherits the configured profile type; fallback vm."})
	set("V1SecretsEnvSetRequest", "env", map[string]any{"type": "object", "minProperties": 1, "additionalProperties": map[string]any{"type": "string"}})
	for _, name := range []string{"V1SandboxDestroyRequest", "V1SandboxStopAllRequest", "V1SandboxSnapshotCreateRequest", "V1SandboxSnapshotRestoreRequest"} {
		set(name, "force", map[string]any{"type": "boolean", "default": false})
	}
	set("V1SandboxReconcileRequest", "apply", map[string]any{"type": "boolean", "default": false})
	set("V1WorkspaceFSCKRequest", "repair", map[string]any{"type": "boolean", "default": false})
	for _, name := range []string{"V1SessionCreateRequest", "V1SessionForkRequest"} {
		definition := defs[name].(map[string]any)
		definition["oneOf"] = []any{nonNullRequired("workspace_id", map[string]any{"type": "string"}), nonNullRequired("workspace_create", map[string]any{"type": "object"})}
	}
	update := defs["V1SandboxUpdateRequest"].(map[string]any)
	set("V1SandboxUpdateRequest", "cores", positive(true))
	set("V1SandboxUpdateRequest", "memory_mb", positive(true))
	update["anyOf"] = []any{nonNullRequired("cores", positive(false)), nonNullRequired("memory_mb", positive(false)), nonNullRequired("tags", map[string]any{"type": "array"})}
	exec := defs["ExecRequest"].(map[string]any)
	exec["anyOf"] = []any{nonNullRequired("command", map[string]any{"type": "string", "pattern": "\\S"}), nonNullRequired("args", map[string]any{"type": "array", "minItems": 1})}
	set("ExecRequest", "timeout", map[string]any{"type": "integer", "default": 300, "description": "Non-positive uses 300 seconds; above 3600 is clamped, not rejected."})
	set("V1IntegrationCreateRequest", "type", map[string]any{"type": "string", "enum": []string{"", "http-proxy", "git-proxy", "llm-proxy"}, "default": "http-proxy"})
	set("V1IntegrationCreateRequest", "secret_type", map[string]any{"type": "string", "enum": []string{"", "bearer", "header", "basic-auth"}, "default": "bearer"})
	set("V1IntegrationCreateRequest", "attach", map[string]any{"type": "string", "default": "auto:all", "description": "Empty selects auto:all; otherwise sandbox:<name>, tag:<value>, or auto:all. Tag selectors are normalized and validated."})
	integration := defs["V1IntegrationCreateRequest"].(map[string]any)
	integration["allOf"] = []any{
		map[string]any{"if": nonNullRequired("type", map[string]any{"const": "git-proxy"}), "else": nonNullRequired("target", map[string]any{"type": "string", "pattern": `\S`})},
		map[string]any{"if": nonNullRequired("type", map[string]any{"const": "llm-proxy"}), "then": map[string]any{"properties": map[string]any{"provider": map[string]any{"type": "string", "enum": []string{"", "openai", "anthropic", "ollama"}}}}, "else": nonNullRequired("secret", map[string]any{"type": "string", "minLength": 1})},
	}
	// Cross-field job workspace constraints follow the handler's nullable-pointer semantics.
	job := defs["V1JobCreateRequest"].(map[string]any)
	job["allOf"] = []any{map[string]any{"not": map[string]any{"allOf": []any{nonNullRequired("workspace_id", map[string]any{"type": "string"}), nonNullRequired("workspace_create", map[string]any{"type": "object"})}}}, map[string]any{"not": map[string]any{"allOf": []any{nonNullRequired("session_id", map[string]any{"type": "string"}), nonNullRequired("workspace_create", map[string]any{"type": "object"})}}}}
}
func nonNullRequired(key string, schema map[string]any) map[string]any {
	return map[string]any{"required": []string{key}, "properties": map[string]any{key: schema}}
}
