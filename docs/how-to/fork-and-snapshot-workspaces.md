# How to fork and snapshot workspaces

Branch a workspace from a point-in-time snapshot. Snapshot and fork let you
test a risky change and throw it away without touching the original workspace
volume.

For the persistence model, see
[Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md).
To recover a broken workspace, see
[Recover a workspace with revert and fsck](recover-with-revert-and-fsck.md).

## Prerequisites

- The workspace on local `zfspool` storage, such as `local-zfs`.
  Snapshot, restore, and copy use the local Proxmox storage library.
- The shell backend, or a loopback API backend with
  `proxmox_api_shell_fallback: true`. Remote API endpoints cannot use a local
  helper. API authentication or permission failures stop before helper execution.
- An unencrypted source for a fork. Encrypted copies require a separate key
  workflow and currently stop before mutation.
- The workspace **detached** from every sandbox. Snapshot, restore, and fork
  all refuse to run while a workspace is attached.

!!! note "Storage and retention"
    Workspace snapshot, fork, and restore are ZFS-only. Automatic snapshot
    retention and pruning limits are not yet documented.

## Steps

1. Detach the workspace so its volume is quiescent.

    ```bash
    agentlab workspace detach dev-workspace
    ```

2. Create a named snapshot you can return to.

    ```bash
    agentlab workspace snapshot create dev-workspace baseline
    agentlab workspace snapshot list dev-workspace
    ```

3. Fork an isolated copy. The original volume is unchanged. Fork from a named
   snapshot to start the copy from a known point.

    ```bash
    agentlab workspace fork dev-workspace --name dev-workspace-exp \
        --from-snapshot baseline
    ```

    To fork from the current state instead, omit `--from-snapshot`.

4. Attach the fork to a new sandbox and run your experiment.

    ```bash
    agentlab sandbox new --profile yolo-workspace \
        --workspace dev-workspace-exp --name exp-box --ttl 1h
    ```

5. Roll the original workspace back to the snapshot when you need to discard
   later changes.

    ```bash
    agentlab workspace detach dev-workspace
    agentlab workspace snapshot restore dev-workspace baseline
    ```

    !!! warning
        Snapshot restore is destructive and has no confirmation prompt. It
        discards every change made since the snapshot. Double-check the
        snapshot name before you run it.

## Verify

List the snapshots and confirm the fork is a separate volume.

```bash
agentlab workspace snapshot list dev-workspace
agentlab workspace list
```

Two workspace records (`dev-workspace` and `dev-workspace-exp`) confirm the
fork succeeded. Files in the original workspace remain at the `baseline`
snapshot state; the experiment is isolated in `dev-workspace-exp`.

## Inspect an interrupted fork

Forks use a full storage stream to create an independent volume. They do not
convert the source into a base image. The destination must be absent.
The receive command does not use a force, recursive, or incremental option.
A current-state fork uses a temporary source snapshot. A named source snapshot
remains after a successful fork. Successful copies remove their temporary
snapshots before the new workspace is published.

AgentLab saves the fork intent before copying. If a response is lost, list the
source's saved operations before another create:

```bash
agentlab workspace fork-status dev-workspace --json
agentlab workspace fork-status dev-workspace workspace-OPERATION --json
```

An operation records its server, owner, source, destination, copy snapshot,
state, and known storage receipt. A GUID is a storage generation identifier.
A verified receipt includes source, snapshot, and target GUIDs.
The operation ID is also the proposed new workspace ID.

`workspace_fork_inspection_required` returns the saved operation in JSON errors.
An unresolved target name stays reserved. Retrying that name as the same actor
resumes the original operation when native recovery is available. Failed copies and failed workspace publication
preserve the target and report any temporary snapshots for inspection.
AgentLab does not delete a volume from its name alone.

`fork-status` needs `workspace.read` and the source workspace's sandbox scope.
Its state is the last saved observation. It does not prove that a worker remains
alive after daemon termination. Client cancellation stops the synchronous helper
and its child processes. Retained volumes do not expire with the request.

Check the `workspace_fork_recovery` capability before requesting recovery:

```bash
agentlab workspace fork-resume dev-workspace workspace-OPERATION --json
```

Recovery requires `workspace.fork`, the source scope, and the original actor.
It uses the original target and allocation reservation. The local helper keeps
a private journal under `/var/lib/agentlab/workspace-copies`. It checks native
GUIDs and local ownership properties before continuing. A complete receive can
finish snapshot cleanup and workspace publication after a lost reply. A named
source snapshot remains. Repeated successful requests return the same workspace.

An incomplete receive, changed GUID, inherited ownership property, or missing
proof blocks recovery. AgentLab preserves the resources for inspection. It does
not overwrite or delete an unverified target. A source lease can remain after
abrupt daemon loss. Recovery waits for its expiration; it does not take over
an active lease. The current lease duration is 30 minutes. Older operations
without a pinned size also require inspection.

These paths have isolated fixture coverage. Live Proxmox and mixed-version
acceptance remain open. Do not infer live storage acceptance from a fake backend.
