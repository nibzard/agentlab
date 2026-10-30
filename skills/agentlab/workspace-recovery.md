# Recover workspace access

Inspect the existing workspace and sandbox before choosing a recovery action.
Workspace data is independent of the disposable VM. Preserve that data unless
the user authorizes its removal.

```bash
agentlab --context "$context" workspace list --json
agentlab --context "$context" workspace check "$workspace" --json
agentlab --context "$context" sandbox show "$vmid" --json
```

Use `workspace rebind` when the authorized task requires attaching existing state
to a new sandbox. Inspect its command help and validate the profile first.
Record the old sandbox, new sandbox, workspace, and leases after the operation.
An interrupted response requires state inspection before another rebind.

```bash
agentlab --context "$context" workspace rebind "$workspace" --profile "$profile" --json
```

`workspace fsck` is a consistency or repair action. Inspect its requirements and
current attachments before running it. Do not assume it is a harmless metadata read.

A sandbox revert changes the guest root disk and can discard task changes.
Collect those changes first. Use revert only within the user's recovery authority.
Do not substitute revert for result collection or use a force option to bypass
an unexplained state conflict.

Use `sandbox update` for an authorized CPU or memory change, then inspect the
returned resource settings. Avoid direct backend commands that skip AgentLab's
state and authorization checks.

```bash
agentlab --context "$context" sandbox update --cores 4 --memory 8GiB "$vmid" --json
agentlab --context "$context" sandbox show "$vmid" --json
```

Destroying a VM is not workspace deletion. Account for retained workspace volumes,
snapshots, leases, and any old VM after recovery.

Check server capabilities before creating snapshots or forks. Native storage
requires a local Proxmox helper and supported storage. Copying an encrypted
source requires a separate key workflow and currently blocks.

After an interrupted fork, inspect its saved operation before repeating the copy.
Use the source workspace and the operation identifier from the error response.
If the identifier is missing, list the source workspace's fork operations.

```bash
agentlab --context "$context" workspace fork-status "$workspace" --json
agentlab --context "$context" workspace fork-status "$workspace" "$operation" --json
```

A saved running state does not prove that a worker remains active. Preserve
recorded target volumes and copy snapshots when inspection is required. A
retained target can contain a verified copy that lacks a workspace record.
Do not delete it or run backend commands to force a retry.

When `workspace_fork_recovery` is true, resume the original operation:

```bash
agentlab --context "$context" workspace fork-resume "$workspace" "$operation" --json
```

Recovery requires the original actor and `workspace.fork`. It checks the original
allocation, native journal, GUIDs, and local ownership properties. Complete copies
can finish cleanup and publication. Partial copies or changed resources remain
blocked. Preserve them and report the operation. An active source lease also
blocks recovery until it expires. Never create a new target to bypass that block.
Snapshot errors can also contain `storage_operation` metadata; preserve those
resources during recovery.
