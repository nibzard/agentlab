# HTTP API reference

Reference for the `agentlabd` control-plane and guest-facing HTTP routes. The control API is versioned under `/v1`, uses `application/json`, and emits timestamps as RFC 3339 Nano. Every listener also serves `GET /healthz`.

!!! tip "Agents: prefer the CLI `--json` surface"
    A coding agent should drive `agentlabd` through the `agentlab` CLI with
    `--json` instead of calling these routes by hand. The CLI prints the daemon
    response verbatim and handles auth, retries, and parsing. See
    [JSON output for agents](agent-json-output.md) and
    [Drive AgentLab as a coding agent](../how-to/drive-agentlab-as-a-coding-agent.md).
    `POST /v1/exec` requires full access and always returns HTTP 200. Branch on
    `exit_code`. Guest runner routes use the bootstrap listener, agent subnet,
    and sandbox secret. They do not accept the control API bearer credential.

## Discovery and status

| Method | Path | Purpose | Response type |
| --- | --- | --- | --- |
| GET | `/v1/schema` | Discover the API and event schema catalog. Returns `api_schema_version`, `event_schema_version`, `resources`, `event_kinds`, `compatibility`. | `schemaResponse` |
| GET | `/v1/capabilities` | Discover compatibility, server identity, features, and caller permissions. Requires `schema.read`. | `protocol.Capabilities` |
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

## Repository snapshots

| Method | Path | Permission | Response |
| --- | --- | --- | --- |
| POST | `/v1/repositories` | `repository.create` | `protocol.RepositorySnapshot` (200) |
| GET | `/v1/repositories/{id}` | `repository.read` | `protocol.RepositorySnapshot` (200) |
| DELETE | `/v1/repositories/{id}` | `repository.delete` | Source ID, `removed:true`, and `cleanup:"verified"` (200) |

These routes reject sandbox-scoped credentials. Each upload belongs to its
acting identity and server. Other identities receive 404 for that source ID.
Upload uses `multipart/form-data` with a `manifest` part followed by a `bundle`
part. No other parts are accepted. The manifest limit is 16 MiB. The bundle
limit is 256 MiB. The host validates content without a worktree checkout.

The response includes the ID, `repo_url`, manifest, creation time, and expiry.
Repeating the same upload returns the original record and expiry. New jobs
cannot use a source after 30 days. Source bytes remain until explicit removal.
Removal returns 409 while a queued or running job references the source.

Submit a snapshot job with the returned `repo_url` and manifest `revision` as
`ref`. This requires `job.create` and `repository.read`. Snapshot jobs use a
stateless VM with an updated guest runner. Workspaces, branch sessions, and
LXC containers are unsupported. Admission pins the source under the same lock
used by removal. Keyed job replay observes the original job after source removal.

Check `repository_upload` and `repository_snapshot_jobs` capabilities first.
The complete `repository_transfer` capability remains false while returned
patches and full result accounting are pending. See
[Prepare a local repository snapshot](../how-to/prepare-local-repository-snapshot.md).

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
| POST | `/v1/bootstrap/register` | recovery (TLS required) | Atomically consume initial token and register unique Ed25519 public key for current incarnation. | `vmid`, `incarnation`, `request_id`, `operation: register`, `token`, base64 raw `public_key` |
| POST | `/v1/bootstrap/challenge` | recovery (TLS required) | Issue random one-minute challenge, bounded per incarnation. | `vmid`, `incarnation`, `request_id` |
| POST | `/v1/bootstrap/recover` | recovery (TLS required) | Consume a signed proof and atomically rotate/retry runtime credentials. | Same scope plus `operation: recover` (or `refresh`), `challenge_id`, `nonce`, base64 `signature` |
| POST | `/v1/bootstrap/execution/start` | recovery (TLS required) | Claim an initial execution attempt exactly once; requires endpoint-secret header. Ambiguous delivery fails closed. | `vmid`, `incarnation`, `request_id` containing the issued `attempt_id` |
| POST | `/v1/bootstrap/fetch` | bootstrap | Single-use token plus VMID delivery of the bootstrap payload. Token is consumed on success. | `{"token":"...","vmid":N}` |
| GET | `/v1/runner/repository?vmid=N&job_id=ID` | bootstrap | Download the pinned source bundle for the authenticated active guest job. Requires the sandbox secret. | Git bundle |
| POST | `/v1/runner/report` | bootstrap | In-guest runner status report. | Runner status event |
| POST | `/v1/sandbox/bootstrap-status` | bootstrap | Authenticated sandbox initialization and independent Tailscale diagnostics. | `vmid`, `runner_ready`, `tailscale_state`, optional fixed `error_code` |
| GET | `/metadata/` | bootstrap | Metadata index. Requires `X-AgentLab-Sandbox-Secret` header. | - |
| GET | `/metadata/identity` | bootstrap | Sandbox identity. Requires `X-AgentLab-Sandbox-Secret` header. | - |
| GET | `/metadata/metadata` | bootstrap | Sandbox key-value metadata. Requires `X-AgentLab-Sandbox-Secret` header. | - |
| GET | `/metadata/secrets/` | bootstrap | Guest secrets metadata. Requires `X-AgentLab-Sandbox-Secret` header. | - |
| ANY | `/proxy/` | bootstrap | Integration credential proxy for sandboxes. Requires `X-AgentLab-Sandbox-Secret` header. | - |
| POST | `/upload` | artifact | Artifact upload, authenticated by a per-job bearer token. | `application/gzip` body |

## Operational endpoints

| Method | Path | Mux | Purpose |
| --- | --- | --- | --- |
| GET | `/healthz` | all | Liveness probe. Registered on the local, control, bootstrap, artifact, and metrics muxes. |
| GET | `/metrics` | metrics | Prometheus exposition. Only when `metrics_listen` is set. |

## Sandbox secret header

Guest-facing endpoints `/metadata/*`, `/proxy/{name}`, `/v1/sandbox/bootstrap-status`, and `/v1/runner/*` require the `X-AgentLab-Sandbox-Secret` header. This header carries a per-sandbox secret that the daemon issues at bootstrap. The daemon stores only the SHA-256 hash of the secret and validates requests with constant-time comparison.

The header prevents a sandbox from spoofing another sandbox's source IP to access its metadata or credential proxies. A request with a valid source IP but an invalid or missing secret is rejected with HTTP 403.

The sandbox image stores the secret at `/run/agentlab/secrets/sandbox-secret` with mode `0600`. Guest code reads that file and sends the header automatically.

Example curl from within a sandbox:

```bash
SANDBOX_SECRET=$(cat /run/agentlab/secrets/sandbox-secret)
curl -H "X-AgentLab-Sandbox-Secret: $SANDBOX_SECRET" \
     http://169.254.169.254/metadata/identity
```

## Authentication and errors

- The local Unix socket is the trusted full-access path and bypasses network auth.
- The remote TCP control listener requires a bearer token (`control_auth_token`) and, for wildcard binds, a CIDR allowlist (`control_allow_cidrs`). The auth middleware accepts SSH-signed tokens from `authorized_keys_path` plus the legacy bearer token.
- Server errors return a stable envelope with `error`, `code`, and `message` fields. Redacted details require the `X-AgentLab-Debug: true` request header.

For the trust model behind these rules, see [security.md](security.md) and [../explanation/control-plane-and-trust-boundaries.md](../explanation/control-plane-and-trust-boundaries.md).

## Related

- [Event contract](event-contract.md) for the event envelope and kinds.
- [Configuration reference](configuration.md) for the keys that enable each listener.
- [Listeners and ports](listeners-and-ports.md) for bind addresses and defaults.

## Managed templates and durable builds

All template routes use control-plane authentication and reject sandbox-scoped
credentials. Permissions are `template.read`, `template.validate`,
`template.create`, `template.cancel`, and `template.activate`; the `template`
namespace grants all five. Trusted Unix-socket and legacy bearer access remain
supported.

| Route | Method | Permission | Response |
| --- | --- | --- | --- |
| `/v1/templates/capabilities` | GET | template.read | Recipes, defaults, limits, prerequisite failures |
| `/v1/templates/validate-plan` | POST | template.validate | Normalized plan, errors and warnings; 422 if invalid |
| `/v1/template-builds` | POST | template.create | 202 build record with ID, VMID and Location |
| `/v1/template-builds` | GET | template.read | `builds`, `next_offset`; optional state filter |
| `/v1/template-builds/{id}` | GET | template.read | Durable build record |
| `/v1/template-builds/{id}/logs` | GET | template.read | `records`, `next_cursor` |
| `/v1/template-builds/{id}/cancel` | POST | template.cancel | Idempotent cancellation request |
| `/v1/templates` | GET | template.read | Successful managed manifests, `next_offset` |
| `/v1/templates/{vmid}` | GET | template.read | Manifest, live existence and drift |
| `/v1/templates/{vmid}/activate` | POST | template.activate | Compare-and-swap profile update |

Create and validate accept `name`, `storage`, optional `vmid`, `recipe`,
`cloudinit_storage`, `bridge`, `memory_mb`, `cores`, `disk_size_gb`, `agent_tools`,
`timeout_seconds`, and the paired `image_url`/`image_sha256` override. Create also
requires a UUID `request_id` and an identical `Idempotency-Key` header. Retrying
that normalized request returns the original record; different input returns
409. No overwrite flag or caller-provided scripts are accepted.

List pagination uses `offset` and `limit` (default 100, maximum 500). Logs use
exclusive `after` sequence and `limit`, returning ascending UTC-timestamped
records. Poll until terminal state, then drain the remaining cursor. Client
request timeouts and disconnects leave execution running in systemd.

States are `QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`. Stages are
`preflight`, `download`, `customize`, `import`, `verify`, `finalize`.
`cancel_requested` is separate durable metadata. Only verified success creates
a manifest. `cleanup_required` and `resources` preserve uncertain ownership or
cleanup failures for manual inspection. An interrupted unit is never rerun.

Activation requires `profile` and `expected_template_vmid`. It validates the
managed template on the local node, rejects configuration drift and incompatible
profile disks, validates the complete YAML profile set, atomically replaces the
file, and publishes a new snapshot. External edits or stale expected VMIDs
return 409. Creation alone never changes profiles.

Stable diagnostics include `template_build_unsupported`,
`template_prerequisite_missing`, `template_vmid_conflict`,
`template_storage_unavailable`, `template_checksum_mismatch`,
`template_build_failed`, `template_build_timeout`, `template_build_interrupted`,
`template_profile_conflict`, and `template_capability_missing`. Queue saturation
returns 503 and `Retry-After: 10`. Validation performs no downloads or writes.

## Guest readiness and enrollment diagnostics

On Proxmox backends, `READY` requires a responding guest agent, a usable guest
IP, and an SSH protocol banner. A running QEMU process or a DHCP lease alone is
insufficient. Provisioning waits up to three minutes within its overall timeout;
recovery also checks before promoting a booting guest. `sandbox show` probes
current readiness and reports `health.guest.ready`, `checked_at`, and an
`error_code` when unavailable. A guest that loses its agent reports
`healthy=false` even while Proxmox still reports the VM running. Readiness
changes are persisted as `sandbox.guest_readiness` events and sampled during
reconciliation, so aggregate status does not infer guest health from QEMU state.
A running guest with no readiness evidence is not reported healthy.

`health.runner_ready` means the runner reported initialization, not continuous
process liveness. `health.tailscale_state` and `health.bootstrap_error` describe
its last authenticated enrollment report. These are independent of VM readiness.
Guest reports accept only fixed diagnostic codes; arbitrary text and enrollment
keys are not persisted. Server-side secret-loading or key-mint failures also
produce bootstrap diagnostics. Missing default bundles are treated as
unconfigured for standalone guests; missing explicit profile bundles remain
errors. Successful enrollment requires an actually valid Tailscale credential.

New template builds reserve two VMIDs atomically: the template and its isolated
boot-verification clone. Stage `boot_verify` checks the guest agent, network,
SSH, runner availability, and unenrolled Tailscale before deleting the clone.
Only then is `manifest.boot_verified=true` recorded with `boot_verified_at`.
Activation rejects historical manifests lacking this evidence; rebuild those
templates. Errors include `template_boot_failed`. Cleanup checks ownership tags,
recorded disks and active tasks, and retains identifiers when manual recovery
is required. No credentials are injected into smoke clones.

## Recovery proof and delivery

Recovery endpoints exist when `guest_recovery_listen` is configured. Requests on
plain HTTP are rejected. They share guest subnet, JSON size, and IP rate limits.
The canonical Ed25519 message is UTF-8 with a trailing newline:

```text
agentlab-guest-recovery-v1
<decimal VMID>
<incarnation>
<challenge ID>
<nonce>
<operation>
<request ID>
```

IDs/nonces are lowercase hex, operations are `recover` or `refresh`, and keys
are base64-encoded raw 32-byte Ed25519 public keys. Generate a new proof for each
attempt. Retrying the same request ID with a fresh challenge returns the same
credential for five minutes, provided no later request superseded it. Cached
responses use AES-GCM with incarnation/request ID as associated data. Expired
rows are pruned on exchanges; neither plaintext credentials nor private guest
keys are stored in SQLite. Concurrent proofs issued for the same credential
generation cannot both rotate it. Same-request retries can recover the winner.

Responses contain `incarnation`, `sandbox_secret`, `credential_expires_at`, and
`job_disposition` (`none`, `execute`, `terminal`, `interrupted`). Only initial
authorized execution carries job execution credentials and an `attempt_id`;
recovery omits git, environment, LLM, artifact, and enrollment credentials.
`refresh` renews authentication without interrupting an active job. A service
restart's `recover` records an unfinished job as failed with
`guest_job_interrupted` and requires an explicit new job. A lost initial response
can be recovered with the guest key registered in that same transaction.

`GET /v1/host` advertises `guest_recovery.enabled`, protocol
`agentlab-guest-recovery-v1`, required template runner `v2`, legacy recovery
`reprovision`, and `native_ssh: false`. This capability does not imply that
existing legacy guests have been migrated.

## Schema field discovery

`GET /v1/schema` includes a `definitions` object. Each resource names its request
and response types. Find their JSON fields under `definitions[type].properties`.
Nested types use `$ref` paths such as `#/definitions/V1SandboxResponse`.
Definitions describe JSON field types. Endpoint validation can impose additional
constraints. Read the endpoint documentation for those constraints.

The execution dry-run endpoint validates the command tree and flags through the
CLI parser. It does not allocate resources or contact the control API. Use
`job validate` or `sandbox validate` to check a provisioning plan against the
host configuration. Install matching CLI and daemon versions for dry-run support.

## Repository result cleanup

`POST /v1/jobs/{id}/result/cleanup` saves an idempotent cleanup retry for a verified
repository result. It requires `job.cleanup` and the job's sandbox scope.
Check `repository_result_cleanup` in capabilities before calling it.

The response contains `job_id`, `accepted`, `repository_result`, and `resources`.
HTTP 202 means pending cleanup is saved. Observe the job for its final outcome.
HTTP 200 with `accepted:false` means cleanup is already complete or retained.
HTTP 409 means result verification or the saved target identity is unavailable.
Retries reuse the original target and never restart the task.

The daemon resumes saved pending cleanup after restart. It checks artifacts,
execution evidence, server identity, sandbox creation time, VM generation,
and guest incarnation. Missing or changed identities block backend deletion.
If the backend VM is absent, recovery updates local records only.

## SSH token revocations

| Method | Path | Permission | Response |
| --- | --- | --- | --- |
| POST | `/v1/token-revocations` | `token.revoke`, global | `protocol.TokenRevocation` (200) |
| GET | `/v1/token-revocations/{id}?issuer=FINGERPRINT` | `token.read`, global | `protocol.TokenRevocation` (200) |

Check `token_revocation` before using these routes. Sandbox-scoped credentials
cannot manage global credential state. POST accepts `issuer`, `revocation_id`,
and expected `server_identity`. It accepts identifiers, not a bearer credential.
A changed server identity returns HTTP 409 before mutation. The response records
the first actor and timestamp; repeated identical requests return the same record.

`revoked:false` means no matching revocation is stored. It does not prove that
a token exists, has a valid signature, remains within its validity period, or
has any requested permission. Revocation records survive restarts and remain
after expiry. The network middleware checks current storage on every SSH-token
request. A lookup failure rejects the request with HTTP 503.

Per-token revocation requires the signed token ID. Tokens without an ID and the
legacy static control credential use their existing key or credential retirement
paths. Already admitted requests and server jobs are not cancelled by revocation.

## Workspace allocation in keyed jobs

`POST /v1/jobs` can include `workspace_create` with an `Idempotency-Key` when
`idempotent_workspace_create` is true. The key retains its existing caller,
server, input, conflict, and expiry rules. A request reserves its workspace
identity before allocating storage. The workspace row and job binding commit
in one transaction. A completed job response replays before profile validation.

A saved complete volume receipt resumes publication without another allocation.
An interrupted allocation without that receipt returns HTTP 409 with
`state:blocked`, `code:workspace_allocation_inspection_required`, an action,
a Boolean `retryable`, and a `workspace_allocation` object. It identifies the
job, workspace, name, storage, size, allocation state, creation time, and any
known volume. Failed publication preserves the volume. Another create request
cannot claim its reserved name while allocation is pending.

Inspect unknown backend outcomes before changing the request key. The API does
not automatically adopt or delete an uncertain volume. A removed committed
workspace is not recreated by a retry. Automatic backend reconciliation and
keyed branch session creation remain incomplete.

New workspace volumes reserve a storage owner ID before allocation. Migration 29
stores these reservations with the shared sandbox and template reservations.
They use IDs from `900000000` through `999999999`. Reservation does not create
a guest. Cluster inventory includes virtual machines and containers on all nodes.
A known owner ID is never replaced because it becomes occupied or changes owner.

Workspace responses include optional `owner_vmid` and `allocation_name` fields
for generated disk names. Allocation error records include the same metadata.
These IDs remain reserved after failed allocation. Keep them free when making
manual Proxmox changes. Existing volume IDs retain their current names.

A backend preflight error proves that no allocation request was sent. Its journal
returns to `planned` with `workspace_allocation_preflight_failed`. Repair the
reported requirement and retry the same key. Errors after the allocation request,
including malformed or mismatched receipts, keep an uncertain allocation.
A disk name is not sufficient proof to adopt or delete an unknown volume.

## Workspace storage and fork operations

`workspace_snapshots` and `workspace_forks` advertise static helper availability.
Each operation still checks its host and storage. The shell backend runs an
embedded fixed program through the installed Proxmox storage library.
The API backend requires a loopback endpoint and explicit local helper access.
It checks API access first. It does not attempt unsupported snapshot or clone
HTTP routes or fall back after API failures.

`POST /v1/workspaces/{id}/fork` saves a durable intent before copying.
Migration 30 stores source and target bindings, actor, server identity, copy
snapshot, progress state, and resource receipts. It does not allocate a temporary
placeholder volume. Success still returns the existing workspace response.
An unsuccessful attempt returns HTTP 409 with a blocked state, an action,
`retryable:false`, and `workspace_fork` when an intent exists.
Its code is `workspace_fork_inspection_required`. A missing local helper returns
`workspace_storage_helper_required` before copying.

`GET /v1/workspaces/{id}/forks` lists saved attempts in `fork_operations`.
`GET /v1/workspaces/{id}/forks/{operation_id}` reads one attempt.
Both require `workspace.read` and retain the source's sandbox scope checks.
The returned state is persisted progress, not a live worker assertion.
`target_state` distinguishes unverified, retained, and published targets.
Overall cleanup remains `inspection_required` for an unpublished target.
A storage receipt can separately confirm temporary snapshot cleanup.

Snapshot errors can include `storage_operation` with source and snapshot IDs.
A failed snapshot publication preserves the snapshot for inspection.
A fork target and temporary snapshots remain accounted for after failure.
Unknown targets are not adopted or deleted automatically. These operations
still need durable worker recovery, automatic reconciliation, verified cleanup,
and live Proxmox acceptance.

## Job test commands

Job creation and plan validation accept optional `test_commands`, an ordered
array of shell command strings. Job views and guest bootstrap preserve it.
The `job_test_commands` capability advertises server support. Commands require
the existing job permissions and run in the existing guest execution boundary.
Validation limits are 16 commands, 4096 bytes per command, and 16384 bytes total.
Empty commands, invalid UTF-8, and NUL characters are invalid. Plan validation
returns an `invalid_field_value` issue for `test_commands`. Job creation rejects
invalid input before reserving a submission or allocating resources.

Migration 31 persists commands with the job. Idempotency input hashing includes
the ordered array. The runner reports separate agent and overall exit codes.
For uploaded repository jobs, result verification binds each reported command
and log digest to the admitted plan. Collection repeats that check locally.
Missing or mismatched evidence prevents verified cleanup. URL-based jobs reject
terminal command reports that omit or change the admitted plan. Their artifact
archives do not receive the uploaded repository verification contract.

## Credential renewal

`POST /v1/token-renewals/prepare` accepts `candidate_token` and expected
`server_identity`. Authenticate with the finite SSH-signed parent. The
`token.renew` permission is required. Sandbox scopes are accepted for this self
operation. The candidate must preserve the issuer, subject, ordered commands,
and sandbox scope. It must be fresh and expire within 24 hours. The request body
is bounded. Credentials are never returned or stored in renewal records.

`POST /v1/token-renewals/commit` accepts `parent_revocation_id` and expected
`server_identity`. Authenticate with the prepared candidate. Commit saves the
receipt and parent revocation atomically. Candidate and parent revocation checks
run inside that transaction. Revocation after request authentication therefore
cannot silently authorize commit. Committed retries preserve the first receipt.
The normal middleware also checks candidate expiry and revocation on every request.

Migration 32 stores issuer-bound renewal identifiers and audit metadata.
`token_renewal` advertises protocol support. Missing permissions return 403.
Invalid candidates return `invalid_renewal_candidate`; conflicting candidates
return `renewal_conflict`. Unprepared commit returns `renewal_not_prepared`.
Revocation blocks commit with `renewal_credential_revoked`. Storage failures return
`renewal_store_unavailable` and retain the original candidate for retry.

Enrollment through the CLI uses the existing authorized SSH key model. It does
not add a server signing secret or a key-registration endpoint. Per-token
revocation remains separate from retirement of the signing key.

### Sandbox capacity preflight

A successful sandbox plan can include `plan.capacity` with `checked`, `cores`,
`memory_mb`, and `allow_burst`. The check uses the same pool as sandbox admission.
It does not reserve resources or return other sandbox allocations. Insufficient
capacity returns `ok: false` with `resource_capacity_unavailable`. Missing pool
configuration or an unspecified resource footprint returns `capacity_not_checked`
as a warning. A passing preflight does not guarantee later admission.

Native SSH provisioning does not require a bootstrap controller URL for standalone
sandbox validation. Other provisioning prerequisites still apply.

The dashboard also serves authenticated `GET /api/connection` on its local
listener. This returns its fixed target and transport without a daemon credential.
It is not a daemon endpoint. A local browser authentication failure includes
`X-Agentlab-Dashboard-Auth: required`. The proxy removes that header from upstream
responses. An upstream authorization failure remains an upstream failure.

### Bind requests to a server

Capabilities include `authorization.issuer` for authenticated actors. For an SSH
credential, this is the verified signing fingerprint. This field does not contain
a credential or private key. `features.request_server_identity` reports support
for binding a request to the server identity.

A remote client can send `X-AgentLab-Server-Identity` with the verified server ID.
The network authentication middleware checks it before dispatching the request.
A mismatch, empty supplied value, or duplicate value returns 409 with
`v1/auth/server_identity_mismatch`. Unavailable local identity returns 503 with
`v1/auth/server_identity_unavailable`. Neither condition reaches the handler.
The header grants no authority. Normal authentication and route policy still apply.
Omitting the header preserves the existing protocol. The local trusted socket
remains a separate trust boundary.

Saved dashboard credential refresh requires this feature and a verified context.
Its local errors include `dashboard_context_changed`,
`dashboard_credential_authority_changed`, `dashboard_credential_lifetime_invalid`,
`dashboard_credential_rejected`, `dashboard_credential_identity_unavailable`,
`dashboard_credential_verification_unavailable`, and `dashboard_credential_unavailable`.
These errors include a corrective action and contain no signed credential.
`GET /api/connection` reports the credential source and whether refresh is enabled.

Verified CLI contexts check compatible capability discovery and request identity
support before JSON, streaming, and repository requests. The resolved identity
remains fixed even if the active context changes. The CLI requires `schema.read`
for these checks. This does not add a permission requirement to the underlying
HTTP operation itself. Existing unbound clients can omit the optional header.
Remote dashboards also verify identity enforcement before starting their listener,
including when their startup credential comes from an explicit override.

### Resume a retained workspace fork

`POST /v1/workspaces/{id}/forks/{operation_id}/resume` resumes the original fork.
It accepts no body and returns the workspace response. Check
`workspace_fork_recovery` in capabilities first. The request requires
`workspace.fork`, the source sandbox scope, and the original actor.

The daemon retains the original target, size, volume reservation, and known GUIDs.
The local helper verifies its private journal and native ownership properties.
A complete copy can finish cleanup and publication after an interrupted reply.
Partial copies, changed identities, and missing proof return a conflict.
The response includes `workspace_fork` metadata when the operation is retained.
A completed operation returns its original workspace on repeated requests.

### Inspect verified execution logs

`GET /v1/jobs/{id}/execution` requires `job.artifacts` and the job's sandbox scope.
Check the `execution_inspection` capability first. The endpoint supports verified
repository snapshot results in a terminal job. Other jobs retain artifact downloads.

Without a `path`, it returns the job, server identity, manifest digest, archive
digest, execution evidence, and entry metadata. Entries include `agent-runner.log`,
`report.json`, and validated test logs. Each entry reports its path, byte size,
SHA-256 digest, and whether its contents are UTF-8 text.

Supply `path` to read one text entry. `offset` is a nonnegative byte offset.
`limit` defaults to 16384 bytes and permits 4 through 65536 bytes. The response
contains `chunk.path`, `offset`, `next_offset`, `eof`, and `text`. Use the returned
`next_offset` for another page. The server preserves UTF-8 character boundaries.
Pass the index's `archive_sha256` on each page to reject a changed archive.

The server checks the complete archive, its digest, report identity, requested
test commands, test log digests, and saved evidence before returning text. It
reads one descriptor beneath the artifact root and never extracts archive files.
Responses disable caching. Invalid queries return 400, missing entries return 404,
and non-UTF-8 text requests return 415. Changed or unverified evidence returns
409 with a corrective action. No rejected response includes a log prefix.
