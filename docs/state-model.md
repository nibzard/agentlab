# Persistent State Model & Safety Guide

This guide explains what persists in AgentLab, how sandboxes, workspaces, and sessions relate, and how to use safety rails (snapshots, forks, fsck, doctor, messagebox) without accidental data loss.

## Mental Model

- **Sandbox VM**: ephemeral VM with an ephemeral root disk. Destroying or reverting the sandbox resets the root filesystem. The sandbox can attach a workspace at `/work`.
- **Workspace volume**: persistent disk mounted at `/work`. This is the only durable filesystem state inside a sandbox. Workspaces can be detached and reattached to new sandboxes.
- **Session**: a logical wrapper that ties a workspace to a profile and tracks the current sandbox VMID. `session resume` creates a new sandbox and reattaches the workspace.

## Relationship Diagram

```mermaid
flowchart LR
    Workspace[/Workspace Volume\n/persisted at /work/]
    Session[Session\n(name, profile, branch)]
    SandboxA[Sandbox VM\n(ephemeral root)]
    SandboxB[New Sandbox VM\n(ephemeral root)]
    RootA[(Root Disk A)]
    RootB[(Root Disk B)]

    Session -- workspace_id --> Workspace
    Session -- current_vmid --> SandboxA
    Workspace -- attach --> SandboxA
    Session -. resume/rebind .-> SandboxB
    Workspace -. reattach .-> SandboxB
    SandboxA -- root --> RootA
    SandboxB -- root --> RootB
```

## Persistence Matrix

| Action | Sandbox root | Workspace `/work` | Session record | Notes |
| --- | --- | --- | --- | --- |
| `sandbox destroy` | deleted | unchanged | unchanged | workspace remains if attached elsewhere |
| `sandbox revert` | reset to `clean` snapshot | unchanged | unchanged | root reset only |
| `session stop` | deleted | unchanged | retained | session keeps workspace binding |
| `session resume` | new root | attached | updated | session points to new VMID |
| `workspace snapshot restore` | unchanged | reverted | unchanged | destructive to workspace data |
| `workspace fork` | unchanged | new copy | unchanged | safe fork for experiments |

## Common Workflows

### Stateful dev loop (workspace + session)

```bash
agentlab session create \
  --name dev-session \
  --profile yolo-workspace \
  --workspace new:dev-workspace \
  --workspace-size 80G

agentlab session resume dev-session
agentlab session show dev-session
```

Use `agentlab session stop dev-session` when you are done. Resume later with `agentlab session resume dev-session` to get a fresh sandbox bound to the same workspace.

### Resume without sessions (manual rebind)

```bash
agentlab workspace rebind dev-workspace --profile yolo-workspace
```

`workspace rebind` creates a new sandbox and attaches the workspace. By default the old sandbox is destroyed unless you pass `--keep-old`.

### Fork for experiments

```bash
agentlab workspace snapshot create dev-workspace baseline
agentlab workspace fork dev-workspace --name dev-workspace-exp --from-snapshot baseline
agentlab session fork dev-session --name exp-session --workspace dev-workspace-exp
```

Forking keeps the original workspace intact and gives you an isolated copy.

### Recover from bad state

Reset the sandbox root to the clean snapshot (workspace untouched):

```bash
agentlab sandbox revert <vmid>
```

Repair a workspace filesystem (read-only by default):

```bash
agentlab workspace detach dev-workspace
agentlab workspace fsck dev-workspace
```

If repairs are required, rerun with `--repair` and reattach the workspace:

```bash
agentlab workspace fsck dev-workspace --repair
agentlab workspace attach dev-workspace <vmid>
```

## Safety Rails for Destructive Operations

- **Sandbox revert**: `agentlab sandbox revert <vmid>` resets the root disk to the `clean` snapshot. Use `--force` only when you are sure no job is running. `--restart` and `--no-restart` control post-revert reboot behavior.
- **Sandbox destroy**: `agentlab sandbox destroy <vmid>` removes the VM. Use `--force` only for stuck or TIMEOUT sandboxes.
- **Workspace snapshot restore**: `agentlab workspace snapshot restore <workspace> <name>` is destructive and requires the workspace to be detached. There is no confirmation prompt; double-check the snapshot name first.
- **Workspace fsck**: `agentlab workspace fsck <workspace>` is read-only. `--repair` modifies the filesystem and should be used only after detaching.
- **Workspace rebind**: destroys the previous sandbox unless `--keep-old` is set.
- **Doctor bundles**: `agentlab sandbox|session|job doctor ...` are read-only diagnostics and safe to run anytime.

## Messagebox Patterns for Multi-Agent Teams

Messagebox is an append-only coordination log scoped to a job, workspace, or session.

Recommended patterns:
- **Session scope** for multi-agent work on the same workspace (handoffs, decisions, next steps).
- **Workspace scope** for long-lived notes that outlive a sandbox.
- **Job scope** for per-run summaries and triage notes.

Examples:

```bash
agentlab msg post --session <session_id> --author alice --kind handoff \
  --text "Paused after tests; see /work/notes.md for context"

agentlab msg post --workspace <workspace_id> --author bob --kind decision \
  --text "Pinned deps; do not bump toolchain without rerunning perf suite"

agentlab msg tail --session <session_id> --follow --tail 50
```

Use `--payload` for structured JSON when coordinating tasks across multiple agents.
