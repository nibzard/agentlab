# HTTP API reference

Reference for the `agentlabd` control-plane and guest-facing HTTP routes. The control API is versioned under `/v1`, uses `application/json`, and emits timestamps as RFC 3339 Nano. Every listener also serves `GET /healthz`.

!!! tip "Agents: prefer the CLI `--json` surface"
    A coding agent should drive `agentlabd` through the `agentlab` CLI with
    `--json` instead of calling these routes by hand. The CLI prints the daemon
    response verbatim and handles auth, retries, and parsing. See
    [JSON output for agents](agent-json-output.md) and
    [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
    Three routes behave differently from the table: `POST /v1/exec` is
    full-access and always returns HTTP 200 (branch on `exit_code`),
    `/v1/integrations` handlers skip authorization, and `/v1/runner/report` is
    served on the guest bootstrap listener (agent subnet, no bearer token).

## Discovery and status

| Method | Path | Purpose | Response type |
| --- | --- | --- | --- |
| GET | `/v1/schema` | Discover the API and event schema catalog. Returns `api_schema_version`, `event_schema_version`, `resources`, `event_kinds`, `compatibility`. | `schemaResponse` |
| GET | `/v1/status` | Control-plane status snapshot, including schema versions. | `V1StatusResponse` |
| GET | `/v1/host` | Host metadata: daemon version, configured subnet, Tailscale DNS name. | `V1HostResponse` |

Both schema versions are `1`. The compatibility policy is additive: unknown event kinds or fields should be ignored by clients.

```bash
agentlab schema
agentlab status
```

## Sandboxes

| Method | Path | Purpose | Request | Response |
| --- | --- | --- | --- | --- |
| POST | `/v1/sandboxes` | Create and provision a sandbox VM. Provisioning is deferred when `job_id` is supplied. | `V1SandboxCreateRequest` | `V1SandboxResponse` (201) |
| POST | `/v1/sandboxes/validate-plan` | Validate a create request without provisioning. | `V1SandboxValidatePlanRequest` | `V1SandboxValidatePlanResponse` |
| GET | `/v1/sandboxes` | List sandbox records from the database. | - | `V1SandboxesResponse` |
| GET | `/v1/sandboxes/inventory` | List live Proxmox VMs annotated with AgentLab and Tailscale metadata. | - | `V1SandboxInventoryResponse` |
| POST | `/v1/sandboxes/reconcile` | Detect or apply (`apply=true`) drift against Proxmox. | `V1SandboxReconcileRequest` | `V1SandboxReconcileResponse` |
| POST | `/v1/sandboxes/stop_all` | Stop every sandbox. | `V1SandboxStopAllRequest` | `V1SandboxStopAllResponse` |
| POST | `/v1/sandboxes/prune` | Remove orphaned sandbox records. | - | `map[string]int` |
| GET | `/v1/sandboxes/{vmid}` | Fetch a sandbox by VMID. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/start` | Start a stopped sandbox. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/stop` | Stop a sandbox without destroying it. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/pause` | Pause a sandbox to SUSPENDED. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/resume` | Resume a SUSPENDED sandbox. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/revert` | Revert the root disk to the clean snapshot. | `V1SandboxRevertRequest` | `V1SandboxRevertResponse` |
| POST | `/v1/sandboxes/{vmid}/update` | Update sandbox resources. | `V1SandboxUpdateRequest` | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/touch` | Update the `LastUsedAt` timestamp. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/destroy` | Destroy a sandbox; `force` bypasses state checks. | - | `V1SandboxResponse` |
| POST | `/v1/sandboxes/{vmid}/lease/renew` | Renew a keepalive lease (RUNNING only; READY returns 409). | `V1LeaseRenewRequest` | `V1LeaseRenewResponse` |
| GET | `/v1/sandboxes/{vmid}/events` | List sandbox events; supports `tail`, `after`, `limit`. | - | `V1EventsResponse` |
| POST | `/v1/sandboxes/{vmid}/doctor` | Create a read-only sandbox doctor bundle. | - | `V1ArtifactUploadResponse` |
| GET | `/v1/sandboxes/{vmid}/snapshots` | List root-disk snapshots. | - | `V1SandboxSnapshotsResponse` |
| POST | `/v1/sandboxes/{vmid}/snapshots` | Create a root-disk snapshot. | `V1SandboxSnapshotCreateRequest` | `V1SandboxSnapshotResponse` |
| POST | `/v1/sandboxes/{vmid}/snapshots/{name}/restore` | Restore a root-disk snapshot. | `V1SandboxSnapshotRestoreRequest` | `V1SandboxSnapshotResponse` |

Only the plural `/snapshots` path is served. The singular `/snapshot` path has no handler and returns 404.

## Jobs

| Method | Path | Purpose | Request | Response |
| --- | --- | --- | --- | --- |
| POST | `/v1/jobs` | Create and start a job. Required: `repo_url`, `profile`, `task`. Defaults `ref=main`, `mode=dangerous`. | `V1JobCreateRequest` | `V1JobResponse` (201) |
| POST | `/v1/jobs/validate-plan` | Validate a job create request without creating resources. | `V1JobValidatePlanRequest` | `V1JobValidatePlanResponse` |
| GET | `/v1/jobs/{id}` | Fetch a job by id; supports `events_tail`. | - | `V1JobResponse` |
| GET | `/v1/jobs/{id}/artifacts` | List artifacts recorded for a job. | - | `V1ArtifactsResponse` |
| GET | `/v1/jobs/{id}/artifacts/download` | Download an artifact by `path` or `name`. | - | `application/octet-stream` |
| POST | `/v1/jobs/{id}/doctor` | Create a read-only job doctor bundle. | - | `V1ArtifactUploadResponse` |

## Workspaces

| Method | Path | Purpose | Request | Response |
| --- | --- | --- | --- | --- |
| POST | `/v1/workspaces` | Create a workspace volume. | `V1WorkspaceCreateRequest` | `V1WorkspaceResponse` |
| GET | `/v1/workspaces` | List workspaces with attached VM and lease metadata. | - | `V1WorkspacesResponse` |
| GET | `/v1/workspaces/{id}` | Fetch a workspace by id or name. | - | `V1WorkspaceResponse` |
| POST | `/v1/workspaces/{id}/attach` | Attach a detached workspace to a sandbox. | `V1WorkspaceAttachRequest` | `V1WorkspaceResponse` |
| POST | `/v1/workspaces/{id}/detach` | Detach a workspace from its sandbox. | - | `V1WorkspaceResponse` |
| POST | `/v1/workspaces/{id}/rebind` | Create a new sandbox and attach the workspace. Destroys the old sandbox unless `keep_old`. | `V1WorkspaceRebindRequest` | `V1WorkspaceRebindResponse` |
| POST | `/v1/workspaces/{id}/fork` | Create an isolated copy. Detached only. | `V1WorkspaceForkRequest` | `V1WorkspaceResponse` |
| GET | `/v1/workspaces/{id}/check` | Run workspace consistency checks. | - | `V1WorkspaceCheckResponse` |
| POST | `/v1/workspaces/{id}/fsck` | Check or repair the filesystem. Detached only. | `V1WorkspaceFSCKRequest` | `V1WorkspaceFSCKResponse` |
| POST | `/v1/workspaces/{id}/lease/clear` | Force-clear stale workspace lease metadata. | - | `V1WorkspaceLeaseClearResponse` |
| GET | `/v1/workspaces/{id}/snapshots` | List workspace volume snapshots. | - | `V1WorkspaceSnapshotsResponse` |
| POST | `/v1/workspaces/{id}/snapshots` | Create a workspace snapshot. Detached only. | `V1WorkspaceSnapshotCreateRequest` | `V1WorkspaceSnapshotResponse` |
| POST | `/v1/workspaces/{id}/snapshots/{name}/restore` | Restore a workspace snapshot. Destructive, detached only. | - | `V1WorkspaceSnapshotResponse` |

## Sessions

| Method | Path | Purpose | Request | Response |
| --- | --- | --- | --- | --- |
| POST | `/v1/sessions` | Create a session binding a workspace to a profile. | `V1SessionCreateRequest` | `V1SessionResponse` |
| GET | `/v1/sessions` | List sessions. | - | `V1SessionsResponse` |
| GET | `/v1/sessions/{id}` | Fetch session details, including `CurrentVMID`. | - | `V1SessionResponse` |
| POST | `/v1/sessions/{id}/resume` | Provision a fresh sandbox and reattach the workspace. | - | `V1SessionResumeResponse` |
| POST | `/v1/sessions/{id}/stop` | Stop the session sandbox; keep the session record and workspace binding. | - | `V1SessionResponse` |
| POST | `/v1/sessions/{id}/fork` | Fork a session onto a new workspace. | `V1SessionForkRequest` | `V1SessionResponse` |
| POST | `/v1/sessions/{id}/doctor` | Create a read-only session doctor bundle. | - | `V1ArtifactUploadResponse` |

## Exposures, messages, and profiles

| Method | Path | Purpose | Request | Response |
| --- | --- | --- | --- | --- |
| GET | `/v1/exposures` | List host-owned Tailscale exposures. | - | `V1ExposuresResponse` |
| POST | `/v1/exposures` | Create an exposure for a sandbox port. | `V1ExposureCreateRequest` | `V1ExposuresResponse` |
| DELETE | `/v1/exposures/{name}` | Remove an exposure by name. | - | `V1Exposure` |
| GET | `/v1/messages` | Read messagebox entries for a scope. Requires `scope_type` and `scope_id`. | - | `V1MessagesResponse` |
| POST | `/v1/messages` | Post a messagebox entry. `scope_type` is `job`, `workspace`, or `session`. | `V1MessageCreateRequest` | `V1Message` |
| GET | `/v1/profiles` | List loaded sandbox profiles. | - | `V1ProfilesResponse` |

## Secrets

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/secrets` | Return a redacted view of the bundle. Raw values are never included. |
| PUT | `/v1/secrets/env` | Merge environment-variable secrets. GET and POST return 405. List with `GET /v1/secrets`. |
| DELETE | `/v1/secrets/env/{key}` | Remove a single environment-variable secret. |
| PUT | `/v1/secrets/git` | Merge git credentials. GET and POST return 405. List with `GET /v1/secrets`. |
| PUT / DELETE | `/v1/secrets/tailscale` | Set or clear the Tailscale enrollment section. GET and POST return 405. |
| POST | `/v1/secrets/ssh-keys` | Add a named SSH public key. GET returns 405. List with `GET /v1/secrets`. |
| DELETE | `/v1/secrets/ssh-keys/{name}` | Remove a named SSH public key. |

See [secrets.md](secrets.md) for the bundle format and token lifecycles.

## Users, teams, integrations, and pool

| Method | Path | Purpose |
| --- | --- | --- |
| GET / POST | `/v1/users` | List or create users. |
| GET / POST / DELETE | `/v1/users/{id}` | Operate on a user resource. |
| GET / POST | `/v1/teams` | List or create teams. |
| GET / POST / DELETE | `/v1/teams/{id}` | Operate on a team resource. |
| GET / POST / DELETE | `/v1/integrations` | Manage integrations. Requires `integrations_enabled`. |
| GET / POST / DELETE | `/v1/integrations/{name}` | Operate on a named integration. |
| GET | `/v1/pool/status` | Return resource-pool over-commit status. |

!!! note "Partially documented surfaces"
    The `/v1/users`, `/v1/teams`, and `/v1/integrations` routes exist and the `user_registry` is wired at daemon init, but the multi-user and team model, RBAC scopes, and the integrations credential shape are not yet documented. Pool over-commit admission behavior behind `/v1/pool/status` is likewise not yet documented.

## Exec API

When `cli_path` is set or auto-detected, the daemon mirrors the CLI over HTTPS.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/v1/exec` | Run an `agentlab` CLI command. |
| POST | `/v1/exec/dry-run` | Dry-run variant of the exec API. |

## Guest-facing endpoints

These run on the bootstrap and artifact listeners, are restricted to the agent subnet, and are rate-limited per source IP. They are not on the `/v1` control plane.

| Method | Path | Mux | Purpose | Request |
| --- | --- | --- | --- | --- |
| POST | `/v1/bootstrap/fetch` | bootstrap | Single-use token plus VMID delivery of the bootstrap payload. Token is consumed on success. | `{"token":"...","vmid":N}` |
| POST | `/v1/runner/report` | bootstrap | In-guest runner status report. | Runner status event |
| GET | `/metadata/` | bootstrap | Metadata index. | - |
| GET | `/metadata/identity` | bootstrap | Sandbox identity. | - |
| GET | `/metadata/metadata` | bootstrap | Sandbox key-value metadata. | - |
| GET | `/metadata/secrets/` | bootstrap | Guest secrets metadata. | - |
| ANY | `/proxy/` | bootstrap | Integration credential proxy for sandboxes. | - |
| POST | `/upload` | artifact | Artifact upload, authenticated by a per-job bearer token. | `application/gzip` body |

## Operational endpoints

| Method | Path | Mux | Purpose |
| --- | --- | --- | --- |
| GET | `/healthz` | all | Liveness probe. Registered on the local, control, bootstrap, artifact, and metrics muxes. |
| GET | `/metrics` | metrics | Prometheus exposition. Only when `metrics_listen` is set. |

## Authentication and errors

- The local Unix socket is the trusted full-access path and bypasses network auth.
- The remote TCP control listener requires a bearer token (`control_auth_token`) and, for wildcard binds, a CIDR allowlist (`control_allow_cidrs`). The auth middleware accepts SSH-signed tokens from `authorized_keys_path` plus the legacy bearer token.
- Server errors return a stable envelope with `error`, `code`, and `message` fields. Redacted details require the `X-AgentLab-Debug: true` request header.

For the trust model behind these rules, see [security.md](security.md) and [../explanation/control-plane-and-trust-boundaries.md](../explanation/control-plane-and-trust-boundaries.md).

## Related

- [Event contract](event-contract.md) for the event envelope and kinds.
- [Configuration reference](configuration.md) for the keys that enable each listener.
- [Listeners and ports](listeners-and-ports.md) for bind addresses and defaults.
