# Workspaces, sessions, and sandboxes

AgentLab splits a coding environment into three layers on purpose: a disposable
VM, a persistent disk, and a record that ties them together. Keeping the three
apart is what lets you destroy and rebuild sandboxes in seconds while your
work survives.

## The three layers

A **sandbox** is an ephemeral Proxmox VE virtual machine. It has an ephemeral
root disk, a guest IP on the agent subnet, a profile, and a lease. When you
destroy or revert a sandbox, the root filesystem resets. Nothing on its root
disk is meant to last.

A **workspace** is a separate volume, mounted inside the sandbox at `/work`.
It is the only durable filesystem state inside a sandbox. You can detach it
from one sandbox and reattach it to another. The git checkout, build caches,
and databases belong on the workspace, not on the root disk.

A **session** is a thin record that binds a workspace to a profile and tracks
the current sandbox VMID. The session does not hold your files; the workspace
does. The session holds the intent: which profile to launch and which
workspace to attach when you resume.

```text
session  --binds-->  workspace (durable /work)  +  profile
                        |
                     attached to
                        |
                     sandbox (ephemeral VM + root disk)
```

## Why split ephemeral from durable

Bundling state into the sandbox VM is tempting, but it punishes you later. VMs
get destroyed when leases expire, when jobs finish, and when you revert a bad
experiment. If your work lives on the root disk, every destroy costs you
progress.

Moving durable state onto a workspace volume makes the sandbox cheap to throw
away. A broken sandbox is not a disaster. You rebind the workspace to a fresh
sandbox, and you are back where you were in the time it takes to clone and
boot a VM.

The split also matches how AI coding agents behave. An agent in "dangerous"
mode can rewrite, install, and delete freely on the root disk without risk to
the project. The workspace carries the source of truth. If the agent leaves
the root disk in a mess, revert the sandbox and keep the workspace.

## How the pieces move

Because the workspace is detachable, the same workspace can outlive many
sandboxes. The session exists to make that loop one command.

When you resume a session, AgentLab provisions a fresh sandbox from the
session profile, reattaches the existing workspace, and updates the session's
current VMID. The old sandbox is gone. The workspace and its files are intact.

If you have no session, you can do the same move by hand with rebind. Rebind
creates a new sandbox and attaches the workspace. By default it destroys the
old sandbox; pass `--keep-old` to keep both.

For experiments that must not touch the original, fork the workspace. A fork
makes an isolated copy of the volume, optionally from a named snapshot. You
can run a risky change on the fork and throw it away without affecting the
source workspace.

## Safety rails

Several operations guard the workspace because it holds real work.

- Snapshot, restore, and fork all require the workspace to be **detached**.
  Restoring a snapshot is destructive and has no confirmation prompt.
- `sandbox revert` rolls back the whole VM to its "clean" snapshot. This is a
  VM-level rollback that can discard changes on the attached workspace disk.
  Detach the workspace first to protect `/work`.
- `session stop` destroys the sandbox but keeps the session record and the
  workspace binding, so you can resume later.

The default stateful workspace size is 80 GB, and the default storage backend
is `local-zfs`. Volume snapshot, restore, and fork are supported on ZFS-backed
storages only.

!!! note "Storage backends"
    Behavior for non-ZFS storage backends (LVM, directory) during snapshot,
    fork, and restore is not yet documented. Treat the snapshot and fork
    commands as ZFS-only until you confirm your storage supports them.

## Where to go next

- Run the loop end to end: [Run a stateful dev session](../tutorials/stateful-dev-session.md).
- Move a workspace by hand: [Use workspaces and rebind](../how-to/use-workspaces-and-rebind.md).
- Branch or copy a workspace: [Fork and snapshot workspaces](../how-to/fork-and-snapshot-workspaces.md).
- Recover from a bad state: [Recover a workspace with revert and fsck](../how-to/recover-with-revert-and-fsck.md).
- Every state and transition: [State machine reference](../reference/state-machine.md).
