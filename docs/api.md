# AgentLab Local Control API (v1)

The local control API is served by `agentlabd` over a Unix domain socket. This API is intended for the `agentlab` CLI and local automation on the host.

- Socket: `/run/agentlab/agentlabd.sock` (configurable)
- Base path: `/v1`
- Content type: `application/json`
- Time format: RFC3339Nano (`time.RFC3339Nano`)

Example curl usage:

```bash
curl --unix-socket /run/agentlab/agentlabd.sock http://localhost/v1/healthz
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

### POST /v1/sandboxes
Create a sandbox record. If `vmid` is omitted, agentlabd allocates the next available VMID starting at 1000 based on its database.

Optional:
- `job_id` attaches the sandbox to an existing job.
- `ttl_minutes` sets `lease_expires_at` from the request time.

### GET /v1/sandboxes
List sandboxes.

### GET /v1/sandboxes/{vmid}
Fetch a sandbox by VMID.

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
