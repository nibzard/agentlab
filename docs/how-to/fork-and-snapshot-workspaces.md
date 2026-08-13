# How to fork and snapshot workspaces

Branch a workspace from a point-in-time snapshot. Snapshot and fork let you
test a risky change and throw it away without touching the original workspace
volume.

For the persistence model, see
[Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md).
To recover a broken workspace, see
[Recover a workspace with revert and fsck](recover-with-revert-and-fsck.md).

## Prerequisites

- The workspace on ZFS-backed storage. The default storage `local-zfs` works.
  Volume snapshot, restore, and clone are ZFS-only; other backends are not
  supported.
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
