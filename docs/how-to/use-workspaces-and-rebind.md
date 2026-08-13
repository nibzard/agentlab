# How to use workspaces and rebind

Attach a persistent workspace volume to a new sandbox VM. A workspace is the
only durable filesystem state inside a sandbox. It is mounted at `/work` and
survives sandbox destruction, so you can move it between ephemeral VMs.

For the persistence model behind this flow, see
[Workspaces, sessions, and sandboxes](../explanation/workspaces-sessions-sandboxes.md).
For state transitions, see
[State machine reference](../reference/state-machine.md).

## Prerequisites

- A running `agentlabd` with a profile whose template supports a secondary
  disk.
- ZFS-backed storage (the default `local-zfs`) if you later want snapshot,
  fork, or restore on the workspace. See
  [Fork and snapshot workspaces](fork-and-snapshot-workspaces.md).

!!! note "Storage backends"
    Workspace snapshot, fork, and restore are implemented for ZFS storage only.
    Behavior on LVM or directory storage is not yet documented.

## Steps

1. Create the workspace volume. The default size used by stateful jobs is
   80 GB.

    ```bash
    agentlab workspace create --name dev-workspace --size 80G
    agentlab workspace list
    ```

2. Create a sandbox and attach the workspace in one step.

    ```bash
    agentlab sandbox new --profile yolo-workspace \
        --workspace dev-workspace --name dev-box --ttl 8h --keepalive
    ```

    Alternatively, attach an existing detached workspace to a running sandbox
    by VMID.

    ```bash
    agentlab workspace attach dev-workspace 1001
    ```

3. When the sandbox is about to expire, rebind the workspace to a fresh
   sandbox. Rebind destroys the old sandbox unless you keep it.

    ```bash
    agentlab workspace rebind dev-workspace --profile yolo-workspace
    ```

    To preserve the old sandbox while you test the new one, add `--keep-old`.

    ```bash
    agentlab workspace rebind dev-workspace --profile yolo-workspace --keep-old
    ```

4. To detach the workspace without destroying the sandbox (for example before a
   filesystem check), use `detach`. The volume remains available for the next
   attach or rebind.

    ```bash
    agentlab workspace detach dev-workspace
    ```

## Verify

Confirm the workspace is attached to the current sandbox.

```bash
agentlab workspace list
agentlab sandbox show 1001
```

The sandbox record lists the attached workspace. Inside the sandbox, files
written under `/work` persist across rebinds; files written anywhere else on the
root disk are lost when the sandbox is destroyed or rebound.
