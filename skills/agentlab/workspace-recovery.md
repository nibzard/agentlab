# Workspace Recovery

## /workspace-rebind
Rebind an existing workspace to a new sandbox after disruption.

```bash
agentlab workspace rebind "<name>" --profile "<profile>"
```

Useful when:
- The prior sandbox exited before job completion.
- You need a continuity point for long-running work.

## /sandbox-revert
Fallback recovery for mutable state:

```bash
agentlab sandbox revert <vmid>
agentlab sandbox revert --no-restart <vmid>
agentlab sandbox revert --force <vmid>
```

## /workspace-fsck
Validate workspace integrity before reuse.

```bash
agentlab workspace fsck <name>
```

## /workspace-destroy cleanup
Prefer workspace destroy only when abandoning state.

```bash
agentlab sandbox destroy --force <vmid>
```

After cleanup, keep snapshots and artifact references until jobs are complete.
