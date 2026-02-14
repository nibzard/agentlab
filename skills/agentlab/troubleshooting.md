# Troubleshooting

## /workspace-check
Validate workspace metadata and status.

```bash
agentlab workspace check <name>
```

## /workspace-fsck
Run consistency checks before forceful workspace actions.

```bash
agentlab workspace fsck <name>
```

## /sandbox-prune
Clean orphaned TIMEOUT/ghost sandboxes when control state diverges.

```bash
agentlab sandbox prune
```

## /sandbox-stop
Use stop controls for recovery scenarios.

```bash
agentlab sandbox stop <vmid>
agentlab sandbox start <vmid>
```

## Restart patterns
- Validate daemon logs first.
- Confirm Proxmox `qm list` and event failures.
- Use host-level status and logs from `agentlab status`.
