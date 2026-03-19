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

## /sandbox-update
Resize an existing sandbox when recovery work needs more CPU or memory.

```bash
agentlab sandbox update --cores 4 --memory 8GiB <vmid>
agentlab sandbox update --memory 12288 <vmid>
agentlab sandbox show <vmid>
```

Use this when:
- a recovery/debug session is constrained by RAM or CPU
- you want an audited control-plane change instead of `qm set`
- you need to verify the new resource shape from the CLI after the update

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
