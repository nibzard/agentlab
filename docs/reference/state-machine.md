# Sandbox, workspace, and job state reference

AgentLab tracks lifecycle state for sandboxes, jobs, workspaces, sessions, and
artifacts. This page lists the formal states and the transitions the daemon
enforces. For the mental model behind these states, see
../explanation/workspaces-sessions-sandboxes.md.

## Sandbox states

A sandbox VM is governed by a finite-state machine in
`internal/daemon/sandbox_manager.go`. The `SandboxState` constants are defined
in `internal/models/models.go`.

| State | Meaning |
| --- | --- |
| `REQUESTED` | Sandbox record created; VM not yet created. |
| `PROVISIONING` | VM is being cloned and configured in Proxmox. |
| `BOOTING` | VM created and booting; waiting for the guest-agent IP. |
| `READY` | VM is running and ready to accept a job. |
| `RUNNING` | A job is actively executing in the sandbox. |
| `SUSPENDED` | VM suspended (paused) but not destroyed. |
| `COMPLETED` | Job finished successfully. |
| `FAILED` | Job failed. |
| `TIMEOUT` | Sandbox lease expired. |
| `STOPPED` | VM stopped but not destroyed. |
| `DESTROYED` | VM destroyed and resources released. Terminal. |

## Sandbox state transitions

`allowedTransition(from, to)` enforces the table below. An illegal transition
returns `ErrInvalidTransition` with the message
`invalid sandbox state transition: <from> -> <to>`.

| From | Allowed target states |
| --- | --- |
| `REQUESTED` | `PROVISIONING`, `TIMEOUT`, `DESTROYED` |
| `PROVISIONING` | `BOOTING`, `TIMEOUT`, `DESTROYED` |
| `BOOTING` | `READY`, `TIMEOUT`, `DESTROYED` |
| `READY` | `RUNNING`, `SUSPENDED`, `STOPPED`, `TIMEOUT`, `DESTROYED` |
| `RUNNING` | `SUSPENDED`, `COMPLETED`, `FAILED`, `TIMEOUT`, `STOPPED`, `DESTROYED` |
| `SUSPENDED` | `READY`, `RUNNING`, `STOPPED`, `TIMEOUT`, `DESTROYED` |
| `COMPLETED` | `STOPPED`, `DESTROYED` |
| `FAILED` | `STOPPED`, `DESTROYED` |
| `TIMEOUT` | `STOPPED`, `DESTROYED` |
| `STOPPED` | `DESTROYED`, `BOOTING`, `READY`, `RUNNING` |
| `DESTROYED` | (terminal, no transitions) |

Notes:

- `agentlab sandbox destroy --force` overrides state restrictions for stuck or
  `TIMEOUT` sandboxes.
- Lease renewal (`agentlab sandbox lease renew --ttl <ttl> <vmid>`) is accepted
  only when the sandbox is `RUNNING`. `READY` is rejected with 409.
- `agentlab sandbox revert` resets the root disk to the `clean` snapshot. It
  does not change workspace state.

## Job status

The `JobStatus` constants are `QUEUED`, `RUNNING`, `COMPLETED`, `FAILED`, and
`TIMEOUT`.

```text
QUEUED -> RUNNING -> (COMPLETED | FAILED | TIMEOUT)
```

A guest-runner exit code of `124` maps to `TIMEOUT`. Job execution drives the
host sandbox through `PROVISIONING` -> `RUNNING`.

## Workspace attachment and lease

Workspaces have no formal state enum. A workspace is either attached to a
sandbox VM (`AttachedVM` set) or detached. The following operations require the
workspace to be detached and fail otherwise with dedicated errors
(`ErrWorkspaceSnapshotAttached`, `ErrWorkspaceForkAttached`,
`ErrWorkspaceFSCKAttached`):

- snapshot create and snapshot restore
- fork
- fsck

Lease metadata fields on the workspace record:

| Field | Purpose |
| --- | --- |
| `LeaseOwner` | Current holder of the workspace lease. |
| `LeaseNonce` | Compare-and-set token used for renew and release. |
| `LeaseExpires` | When the workspace lease expires. |

`agentlab workspace lease clear <workspace>` force-clears stale lease metadata.

!!! warning
    Workspace snapshot restore is destructive and has no confirmation prompt.
    Double-check the snapshot name first.

## Sessions

A session has no formal state enum. It binds a workspace to a profile and
tracks the current sandbox VMID (`CurrentVMID`). Operations:

| Operation | Effect |
| --- | --- |
| `session create` | Bind a workspace to a profile and start a sandbox. |
| `session resume` | Create a new sandbox, reattach the workspace, update `CurrentVMID`. |
| `session stop` | Delete the sandbox root; keep the session record and workspace binding. |
| `session fork` | Fork the session onto a new workspace. |
| `session branch` | Create or switch to a deterministic branch-derived session. |

## Artifacts

Artifacts follow an upload-then-store flow with no formal state enum:

1. The guest runner tars logs and `report.json` into
   `agentlab-artifacts.tar.gz` and uploads it with `POST /upload` using a
   per-job bearer token.
2. The daemon stores the bundle under `artifact_dir` (default
   `/var/lib/agentlab/artifacts`).
3. Artifacts are listed and downloaded per job.

!!! warning "Gap"
    Artifact retention and garbage collection are not yet documented. An
    `artifactGC` manager runs in the daemon, but its schedule and limits are
    not described in source or docs.

## Doctor bundles

`agentlab sandbox doctor`, `agentlab session doctor`, and `agentlab job doctor`
produce read-only diagnostic bundles. They are safe to run at any time and do
not change state.

## Reconciliation

A reconciler syncs the database with live Proxmox VMs. Rules:

- VM not found in Proxmox -> mark `DESTROYED`.
- VM stopped but recorded `RUNNING` -> mark `FAILED`.
- VM running but recorded `REQUESTED` -> mark `READY`.

See ../explanation/reconciliation-and-shutdown.md for the loop and the shutdown
drain. For idle-stop and lease behavior, see
../explanation/idle-stop-and-lease-model.md. For stuck-state recovery, see
../how-to/debug-a-stuck-sandbox.md.

## Metrics

Every sandbox state transition increments an
`agentlab_sandbox_transitions_total` counter. See reference/metrics.md.
