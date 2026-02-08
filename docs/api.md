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
{ "error": "message" }
```

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
  "keepalive": false
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
  "status": "QUEUED",
  "sandbox_vmid": 1000,
  "created_at": "2026-01-29T23:45:00Z",
  "updated_at": "2026-01-29T23:45:00Z"
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

## Endpoints

### POST /v1/jobs
Create a job record.

- Required: `repo_url`, `profile`, `task`
- Defaults: `ref=main`, `mode=dangerous`

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
    { "id": 10, "ts": "2026-01-30T03:14:00Z", "kind": "job.report", "job_id": "job_0123abcd", "msg": "bootstrapped" }
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

Notes:
- If `job_id` is provided, the sandbox record is created and attached to the job, but provisioning is deferred to the job runner.
- The request may take time while the VM boots.

Optional:
- `job_id` attaches the sandbox to an existing job.
- `ttl_minutes` sets `lease_expires_at` from the request time.

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
