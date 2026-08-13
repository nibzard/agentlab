# Run a stateful dev session

Goal: bind a persistent workspace to a profile with a session, so your work survives when the sandbox VM is replaced. You will create a session, resume it on a fresh sandbox, and branch it for an experiment.

## Prerequisites

- A working sandbox setup, as described in [Create your first sandbox](create-first-sandbox.md).
- A profile that attaches a workspace. The bundled `yolo-workspace` profile sets `storage.workspace: attach` and `repo.clone_path: /work/repo`.
- ZFS storage (default `local-zfs`) so the workspace volume supports snapshots and forks.

## Steps

1. Create a session and provision its first sandbox. `session create` records the session and creates the workspace volume, but it does not provision a sandbox. `session resume` provisions the sandbox and attaches the workspace at `/work`.

    ```bash
    agentlab session create --name dev-session --profile yolo-workspace --workspace new:dev-workspace --workspace-size 80G
    agentlab session resume dev-session
    ```

   The workspace is the only durable filesystem state inside the sandbox. The sandbox root disk stays ephemeral.

2. Confirm the session is up and note the current sandbox VMID.

    ```bash
    agentlab session show dev-session
    ```

3. Make some changes in the workspace, then stop the session sandbox. Stopping deletes the ephemeral root disk but keeps the workspace volume and the session record.

    ```bash
    agentlab session stop dev-session
    ```

4. Resume the session. Resume provisions a fresh sandbox and reattaches the same workspace, so your files at `/work` come back exactly as you left them.

    ```bash
    agentlab session resume dev-session
    ```

5. Before a risky change, snapshot the workspace so you can roll it back. Snapshot and restore require the workspace to be detached and unleased. `session stop` destroys the sandbox and detaches the workspace, but it also holds a lease on the workspace, so clear that lease before you snapshot.

    ```bash
    agentlab session stop dev-session
    agentlab workspace lease clear dev-workspace
    agentlab workspace snapshot create dev-workspace baseline
    ```

   Restore is destructive and has no confirmation prompt, so check the snapshot name carefully:

    ```bash
    agentlab workspace snapshot restore dev-workspace baseline
    ```

6. Branch the session for an experiment. `session branch` derives a deterministic session name from the branch name, so you can switch between branches the same way you switch git branches.

    ```bash
    agentlab session branch feature/login --profile yolo-workspace
    ```

## Expected result

`agentlab session show dev-session` reports the current sandbox VMID, the bound workspace, and the profile. After `session stop` the sandbox VM is gone but the workspace and session record remain. After `session resume` a new VMID is bound to the session and the workspace contents at `/work` are intact.

Use the message box to leave yourself or a teammate a handoff note scoped to the session:

```bash
agentlab msg post --session dev-session --author alice --kind handoff --text "Paused after tests"
agentlab msg tail --session dev-session --follow
```

## Next

For the persistence model behind sandboxes, workspaces, and sessions, see [Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md). For branching workspaces without a session, see [Use workspaces and rebind](../how-to/use-workspaces-and-rebind.md) and [Fork and snapshot workspaces](../how-to/fork-and-snapshot-workspaces.md).
