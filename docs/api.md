# AgentLab Local Control API (v1)

The local control API is served by `agentlabd` over a Unix domain socket. This API is intended for the `agentlab` CLI and local automation on the host.

- Socket: `/run/agentlab/agentlabd.sock` (configurable)
- Base path: `/v1`
- Content type: `application/json`
- Time format: RFC3339Nano (`time.RFC3339Nano`)

Example curl usage:

```bash
curl --unix-socket /run/agentlab/agentlabd.sock http://localhost/healthz
```

## Error shape

```json
{ "error": "message", "code": "v1/...", "message": "machine-readable canonical message", "details": "redacted details if enabled" }
```

`code` and `message` are stable, machine-readable fields. `error` remains populated for compatibility.
By default, server errors (`5xx`) return only `error` and `message`/`code`.
For debugging, include `X-AgentLab-Debug: true` to request redacted `details` on `5xx` responses.

## Versioned types

```json
// V1JobCreateRequest
{
  "repo_url": "https://github.com/org/repo.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "fix the failing tests",
  "mode": "dangerous",
  "ttl_minutes": 120,
  "keepalive": false,
  "workspace_id": "workspace-001",
  "workspace_wait_seconds": 60
}
```

```json
// V1JobResponse
{
  "id": "job_0123abcd",
  "repo_url": "https://github.com/org/repo.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "fix the failing tests",
  "mode": "dangerous",
  "ttl_minutes": 120,
  "keepalive": false,
  "workspace_id": "workspace-001",
  "status": "QUEUED",
  "sandbox_vmid": 1000,
  "created_at": "2026-01-29T23:45:00Z",
  "updated_at": "2026-01-29T23:45:00Z"
}
```

```json
// V1PreflightIssue
{
  "code": "missing_required_field",
  "field": "repo_url",
  "message": "repo_url is required"
}
```

```json
// V1JobValidatePlanRequest
{
  "repo_url": "https://github.com/org/repo.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "fix the failing tests",
  "mode": "dangerous",
  "ttl_minutes": 120,
  "keepalive": false,
  "workspace_id": "workspace-001",
  "workspace_wait_seconds": 60,
  "session_id": "sess_1234abcd"
}
```

```json
// V1JobValidatePlan
{
  "repo_url": "https://github.com/org/repo.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "fix the failing tests",
  "mode": "dangerous",
  "ttl_minutes": 120,
  "keepalive": false,
  "workspace_id": "workspace-001",
  "workspace_wait_seconds": 60,
  "session_id": "sess_1234abcd"
}
```

```json
// V1JobValidatePlanResponse
{
  "ok": true,
  "errors": [],
  "warnings": [
    {
      "code": "job_ref_defaulted",
      "field": "ref",
      "message": "ref was defaulted to main"
    }
  ],
  "plan": {
    "repo_url": "https://github.com/org/repo.git",
    "ref": "main",
    "profile": "yolo-ephemeral",
    "task": "fix the failing tests",
    "mode": "dangerous",
    "ttl_minutes": 120,
    "keepalive": false,
    "workspace_id": "workspace-001",
    "workspace_wait_seconds": 60,
    "session_id": "sess_1234abcd"
  }
}
```

```json
// V1SandboxCreateRequest
{
  "name": "sandbox-1000",
  "profile": "yolo-ephemeral",
  "keepalive": false,
  "ttl_minutes": 180,
  "workspace_id": "workspace-001",
  "vmid": 1000,
  "job_id": "job_0123abcd"
}
```

```json
// V1SandboxResponse
{
  "vmid": 1000,
  "name": "sandbox-1000",
  "profile": "yolo-ephemeral",
  "state": "REQUESTED",
  "ip": "10.77.0.10",
  "workspace_id": "workspace-001",
  "keepalive": false,
  "lease_expires_at": "2026-01-30T02:45:00Z",
  "created_at": "2026-01-29T23:45:00Z",
  "updated_at": "2026-01-29T23:45:00Z"
}
```

```json
// V1SandboxValidatePlanRequest
{
  "name": "sandbox-1000",
  "profile": "yolo-ephemeral",
  "keepalive": false,
  "ttl_minutes": 180,
  "workspace_id": "workspace-001",
  "vmid": 1000,
  "job_id": "job_0123abcd"
}
```

```json
// V1SandboxValidatePlan
{
  "name": "sandbox-1000",
  "profile": "yolo-ephemeral",
  "keepalive": false,
  "ttl_minutes": 180,
  "workspace_id": "workspace-001",
  "vmid": 1000,
  "job_id": "job_0123abcd"
}
```

```json
// V1SandboxValidatePlanResponse
{
  "ok": true,
  "errors": [],
  "warnings": [],
  "plan": {
    "name": "sandbox-1000",
    "profile": "yolo-ephemeral",
    "keepalive": false,
    "ttl_minutes": 180,
    "workspace_id": "workspace-001",
    "vmid": 1000,
    "job_id": "job_0123abcd"
  }
}
```

```json
// V1Profile
{
  "name": "yolo-ephemeral",
  "template_vmid": 9000,
  "updated_at": "2026-01-29T23:45:00Z"
}
```

```json
// V1ProfilesResponse
{
  "profiles": [
    {
      "name": "yolo-ephemeral",
      "template_vmid": 9000,
      "updated_at": "2026-01-29T23:45:00Z"
    }
  ]
}
```

```json
// V1SandboxRevertRequest
{
  "force": false,
  "restart": true
}
```

```json
// V1SandboxRevertResponse
{
  "snapshot": "clean",
  "was_running": true,
  "restarted": true,
  "sandbox": {
    "vmid": 1000,
    "name": "sandbox-1000",
    "profile": "yolo-ephemeral",
    "state": "RUNNING",
    "created_at": "2026-01-29T23:45:00Z",
    "updated_at": "2026-01-29T23:45:00Z"
  }
}
```

```json
// V1WorkspaceCreateRequest
{
  "name": "workspace-alpha",
  "size_gb": 80,
  "storage": "local-zfs"
}
```

```json
// V1WorkspaceResponse
{
  "id": "workspace-0123abcd",
  "name": "workspace-alpha",
  "storage": "local-zfs",
  "volid": "local-zfs:vm-0-disk-0",
  "size_gb": 80,
  "attached_vmid": 1000,
  "created_at": "2026-01-29T23:45:00Z",
  "updated_at": "2026-01-29T23:45:00Z"
}
```

```json
// V1ExposureCreateRequest
{
  "name": "sbx-1000-8080",
  "vmid": 1000,
  "port": 8080
}
```

```json
// V1Exposure
{
  "name": "sbx-1000-8080",
  "vmid": 1000,
  "port": 8080,
  "target_ip": "10.77.0.10",
  "url": "tcp://host.tailnet.ts.net:8080",
  "state": "serving",
  "created_at": "2026-02-08T20:30:00Z",
  "updated_at": "2026-02-08T20:30:00Z"
}
```

```json
// V1ExposuresResponse
{
  "exposures": [
    {
      "name": "sbx-1000-8080",
      "vmid": 1000,
      "port": 8080,
      "target_ip": "10.77.0.10",
      "url": "tcp://host.tailnet.ts.net:8080",
      "state": "serving",
      "created_at": "2026-02-08T20:30:00Z",
      "updated_at": "2026-02-08T20:30:00Z"
    }
  ]
}
```

```json
// V1LeaseRenewRequest
{ "ttl_minutes": 240 }
```

```json
// V1LeaseRenewResponse
{ "vmid": 1000, "lease_expires_at": "2026-01-30T03:45:00Z" }
```

## Event contract

Daemon event records use a shared JSON envelope in `V1Event.json`:

```json
{
  "kind": "sandbox.start.completed",
  "schema_version": 1,
  "stage": "lifecycle",
  "payload": {
    "duration_ms": 512,
    "result": "ok"
  }
}
```

`schema_version` and `stage` allow clients to consume events with predictable semantics even as payload fields evolve.
Events emitted by daemon versions before the canonical contract continue to surface as plain JSON payloads and are treated as `schema_version: 0` for compatibility.

Current canonical stages:

- `lifecycle` - state transitions and lifecycle completion/failure events
- `lease` - lease allocation and renewal flows
- `slo` - startup SLO measurements
- `recovery` - stop/start fallback and restore operations
- `snapshot` - snapshot create/restore activity
- `report` - periodic job runner reports
- `network` - IP and network assignment milestones
- `artifact` - artifact upload/gc lifecycle
- `exposure` - exposure create/delete lifecycle

Canonical kinds are grouped by domain in `internal/daemon/event_types.go`:

- `sandbox.*`
- `job.*`
- `workspace.*` (including lease and recovery operations)
- `artifact.*`
- `exposure.*`

Clients should rely on `kind`, `schema_version`, and `stage` as the stable contract boundary.

## Schema discovery

`agentlab` can fetch machine-readable API metadata at `GET /v1/schema` (or run `agentlab schema` from the CLI). Use this endpoint for contract discovery and offline client validation.

Response fields:

- `generated_at`: RFC3339Nano timestamp for the schema snapshot.
- `api_schema_version`: active API schema version.
- `event_schema_version`: active event schema version.
- `resources`: array of endpoint contracts:
  - `path`: URL path template.
  - `methods`: allowed methods.
  - `request_type`/`response_type`: JSON type names where applicable.
  - `notes`: optional compatibility or behavior notes.
- `event_kinds`: canonical event kinds with required/optional payload field metadata.
- `compatibility`: versioning policy used for additive/breaking changes.

Example output:

```json
{
  "generated_at": "2026-02-14T12:00:00Z",
  "api_schema_version": 1,
  "event_schema_version": 1,
  "resources": [
    {
      "path": "/v1/status",
      "methods": ["GET"],
      "summary": "Fetch control-plane status",
      "response_type": "V1StatusResponse"
    }
  ],
  "compatibility": {
    "api": "Additive endpoint, path, and optional field changes are preferred. Breaking changes bump the API schema version.",
    "events": "Event kinds, required payload fields, and required version values are managed as an additive contract.",
    "errors": "Unknown event kinds or fields should be ignored by clients."
  }
}
```

### Compatibility policy

- `api`: Additive endpoint/path/optional-field changes are preferred; breaking API changes bump `api_schema_version`.
- `events`: Event kind and payload evolution is additive with required schema version checks.
- `errors`: Unknown event kinds or fields should be ignored by clients.

## Endpoints

### GET /v1/schema
Fetch the machine-readable API and event contract catalog.

### GET /v1/status
Fetch control-plane status and schema versions:

- `api_schema_version`
- `event_schema_version`

### POST /v1/jobs
Create a job record.

- Required: `repo_url`, `profile`, `task`
- Defaults: `ref=main`, `mode=dangerous`
- Optional workspace selection fields: `workspace_id` (id or name), `workspace_create` (new workspace `{ "name": "...", "size_gb": 80, "storage": "local-zfs" }`), `workspace_wait_seconds` (wait for detach; 409 on timeout). `workspace_id` and `workspace_create` are mutually exclusive.
- Optional session binding: `session_id` attaches to session workspace and inherits workspace when omitted.

### POST /v1/jobs/validate-plan
Validate a job create request without creating resources.

- Returns
  - `ok`: whether the request can be executed
  - `errors`: ordered list of blocking issues
  - `warnings`: ordered list of non-blocking issues
  - `plan`: normalized request when `ok=true`
- Response `errors` and `warnings` entries use:
  - `code` stable error code
  - `field` logical field pointer
  - `message` deterministic message

Example request:

```json
{
  "repo_url": "https://github.com/org/repo.git",
  "profile": "yolo-ephemeral",
  "task": "fix the failing tests",
  "ttl_minutes": 120
}
```

Example response:

```json
{
  "ok": true,
  "errors": [],
  "warnings": [
    {
      "code": "job_ref_defaulted",
      "field": "ref",
      "message": "ref was defaulted to main"
    }
  ],
  "plan": {
    "repo_url": "https://github.com/org/repo.git",
    "ref": "main",
    "profile": "yolo-ephemeral",
    "task": "fix the failing tests",
    "mode": "dangerous",
    "ttl_minutes": 120,
    "keepalive": false,
    "workspace_id": "workspace-001"
  }
}
```

### GET /v1/jobs/{id}
Fetch a job by id.

Query params:
- `events_tail=<n>` returns the last N events for the job (default 50, max 1000). Use `0` to omit events.

Response includes `events` when requested or by default:

```json
{
  "id": "job_0123abcd",
  "status": "RUNNING",
  "events": [
    {
      "id": 10,
      "ts": "2026-01-30T03:14:00Z",
      "kind": "job.report",
      "schema_version": 1,
      "stage": "report",
      "job_id": "job_0123abcd",
      "msg": "bootstrapped",
      "json": { "status": "running", "artifacts": ["artifacts.tar.gz"] }
    }
  ]
}
```

### GET /v1/jobs/{id}/artifacts
List artifacts recorded for a job.

Response:

```json
{
  "job_id": "job_0123abcd",
  "artifacts": [
    {
      "name": "agentlab-artifacts.tar.gz",
      "path": "agentlab-artifacts.tar.gz",
      "size_bytes": 1048576,
      "sha256": "0123abcd...",
      "mime": "application/gzip",
      "created_at": "2026-01-30T03:15:00Z"
    }
  ]
}
```

### GET /v1/jobs/{id}/artifacts/download
Download a stored artifact file.

Query params:
- `path=<relative path>` downloads an exact artifact path.
- `name=<artifact name>` downloads the latest artifact with that name.
- If neither is provided, the latest artifact is returned.

### GET /v1/profiles
List loaded profiles.

Response:

```json
{
  "profiles": [
    {
      "name": "yolo-ephemeral",
      "template_vmid": 9000,
      "updated_at": "2026-01-29T23:45:00Z"
    }
  ]
}
```

### POST /v1/sandboxes
Create and provision a sandbox VM. If `vmid` is omitted, agentlabd allocates the next available VMID starting at 1000 based on its database. Provisioning clones the template, writes a cloud-init snippet, applies profile resources, starts the VM, and records the guest IP.

For provisioning failures, add `X-AgentLab-Debug: true` to include redacted failure details in the HTTP response payload.

Notes:
- If `job_id` is provided, the sandbox record is created and attached to the job, but provisioning is deferred to the job runner.
- The request may take time while the VM boots.

Optional:
- `job_id` attaches the sandbox to an existing job.
- `ttl_minutes` sets `lease_expires_at` from the request time.

### POST /v1/sandboxes/validate-plan
Validate a sandbox create request without provisioning resources.

- Returns
  - `ok`: whether the request can be executed
  - `errors`: ordered list of blocking issues
  - `warnings`: ordered list of non-blocking issues
  - `plan`: normalized request when `ok=true`
- Response `errors` and `warnings` entries use:
  - `code` stable error code
  - `field` logical field pointer
  - `message` deterministic message

Example request:

```json
{
  "profile": "yolo-ephemeral",
  "ttl_minutes": 120,
  "workspace_id": "workspace-001",
  "vmid": 1000
}
```

Example response:

```json
{
  "ok": true,
  "errors": [],
  "warnings": [],
  "plan": {
    "name": "sandbox-1000",
    "profile": "yolo-ephemeral",
    "keepalive": false,
    "ttl_minutes": 120,
    "workspace_id": "workspace-001",
    "vmid": 1000
  }
}
```

### GET /v1/sandboxes
List sandboxes.

### GET /v1/sandboxes/{vmid}
Fetch a sandbox by VMID.

### POST /v1/sandboxes/{vmid}/revert
Revert a sandbox to the canonical `clean` snapshot.

Body (optional fields):

```json
{ "force": false, "restart": true }
```

Notes:
- When `restart` is omitted, the sandbox is restarted only if it was running.
- Use `force=true` to bypass running-job safety checks.

### POST /v1/sandboxes/{vmid}/destroy
Destroy a sandbox. The response is the updated sandbox record.

### POST /v1/sandboxes/{vmid}/lease/renew
Renew a keepalive sandbox lease.

Body:

```json
{ "ttl_minutes": 240 }
```

### GET /v1/sandboxes/{vmid}/events
List events recorded for a sandbox.

Query params (mutually exclusive `tail`/`after`):
- `tail=<n>` returns the last N events (default used by CLI logs).
- `after=<id>` returns events with id greater than `after` (for follow).
- `limit=<n>` caps the number of events (default 200, max 1000).

Each event follows the same event contract shape above, including `kind`, `schema_version`, `stage`, and `json` payload.

### POST /v1/messages
Post a message to the shared messagebox.

Body:

```json
{
  "scope_type": "job",
  "scope_id": "job_0123abcd",
  "author": "alice",
  "kind": "note",
  "text": "handoff: run tests next",
  "json": { "priority": "high" }
}
```

Notes:
- `scope_type` must be one of `job`, `workspace`, `session`.
- `text` or `json` is required.

### GET /v1/messages
List messages for a scope.

Query params:
- `scope_type=<job|workspace|session>` (required).
- `scope_id=<id>` (required).
- `after_id=<id>` returns messages with id greater than `after_id`.
- `limit=<n>` caps the number of messages (default 200, max 1000).

When `after_id` is omitted, the API returns the most recent `limit` messages in chronological order.

### POST /v1/exposures
Create a host-owned exposure for a sandbox port. The daemon installs the exposure
using host-level Tailscale Serve and records an audit event.

Body:

```json
{ "name": "sbx-1000-8080", "vmid": 1000, "port": 8080 }
```

Notes:
- If `target_ip` is omitted, the sandbox IP is used.
- `state` reflects health checks (`serving`, `healthy`, `unhealthy`).
- The CLI uses `sbx-<vmid>-<port>` for exposure names by default.

### GET /v1/exposures
List exposures.

### DELETE /v1/exposures/{name}
Remove an exposure by name.

### POST /v1/workspaces
Create a workspace volume.

Body:

```json
{ "name": "workspace-alpha", "size_gb": 80, "storage": "local-zfs" }
```

### GET /v1/workspaces
List workspaces.

### GET /v1/workspaces/{id}
Fetch a workspace by id or name.

### POST /v1/workspaces/{id}/attach
Attach a workspace volume to a sandbox VM.

Body:

```json
{ "vmid": 1000 }
```

### POST /v1/workspaces/{id}/detach
Detach a workspace volume from its attached VM.

### POST /v1/workspaces/{id}/rebind
Create a new sandbox from a profile and attach the workspace.

Body:

```json
{ "profile": "yolo-workspace", "ttl_minutes": 240, "keep_old": false }
```

Response:

```json
{
  "workspace": { "id": "workspace-0123abcd", "name": "workspace-alpha" },
  "sandbox": { "vmid": 1001, "profile": "yolo-workspace", "state": "RUNNING" },
  "old_vmid": 1000
}
```

### GET /v1/workspaces/{id}/snapshots
List workspace snapshots.

### POST /v1/workspaces/{id}/snapshots
Create a snapshot for a workspace volume.

Body:

```json
{ "name": "baseline" }
```

Notes:
- Workspaces must be detached before snapshotting.
- Snapshot creation acquires a short-lived workspace lease to enforce single-writer safety.

### POST /v1/workspaces/{id}/snapshots/{name}/restore
Restore a workspace volume to a named snapshot.

Notes:
- Workspaces must be detached before restore.
- Restores are destructive and replace the current volume contents.
