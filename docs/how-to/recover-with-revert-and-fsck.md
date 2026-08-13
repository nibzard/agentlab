# How to recover a workspace with revert and fsck

Recover from a bad state without losing workspace data. Use sandbox `revert` to
reset an ephemeral root disk, and use workspace `fsck` to repair a detached
workspace volume.

For the state machine that governs these operations, see
[State machine reference](../reference/state-machine.md). For the persistence
model, see
[Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md).

## Prerequisites

- The VMID of the sandbox you want to reset, or the workspace you want to
  repair.
- A running `agentlabd`.
- For `fsck`, the workspace must be detached from every sandbox. The command
  refuses to run while a volume is attached.

## Steps

### Reset a sandbox with revert

If a sandbox root disk is in a bad state (a broken package install, a stuck
boot, a misconfigured service), reset it to the clean snapshot. Revert rolls
the whole VM back to the clean snapshot. If a workspace volume is attached,
that disk is part of the snapshot and is reset too. Detach the workspace first
if you need to keep it.

```bash
agentlab sandbox revert 1001 --force --restart
```

- `--force` overrides invalid-state guards.
- `--restart` reboots the sandbox after the revert. `--restart` and
  `--no-restart` are mutually exclusive; pick one. Omit both to restart the
  sandbox only if it was running before the revert. Pass `--no-restart` to
  leave it stopped.

### Repair a workspace with fsck

1. Detach the workspace so the volume is not in use.

    ```bash
    agentlab workspace detach dev-workspace
    ```

2. Check the workspace state against the daemon database and Proxmox first.
   `workspace check` reconciles records; it does not inspect the filesystem.

    ```bash
    agentlab workspace check dev-workspace
    ```

3. Run a read-only filesystem check to see whether the volume is damaged.

    ```bash
    agentlab workspace fsck dev-workspace
    ```

4. If the check reports errors, run the repair pass.

    ```bash
    agentlab workspace fsck dev-workspace --repair
    ```

5. Reattach the repaired workspace to a sandbox.

    ```bash
    agentlab workspace attach dev-workspace 1001
    ```

!!! warning
    The backend does not force-stop VMs before `fsck`. Always detach the
    workspace first. Running `fsck --repair` on a mounted volume corrupts the
    filesystem.

## Verify

Confirm the sandbox reached `RUNNING` after the revert and the workspace
re-attached cleanly.

```bash
agentlab sandbox show 1001
agentlab workspace list
```

After `revert`, the sandbox root disk is back at the clean snapshot. Files
under `/work` are intact only if you detached the workspace before the revert.
After `fsck --repair`, the workspace attaches without errors and the files
written before the incident are readable.
