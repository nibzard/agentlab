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

## /sandbox-show
Inspect the current sandbox record and resource settings before escalating.

```bash
agentlab sandbox show <vmid>
agentlab sandbox show <vmid> --json
```

Check:
- state drift between AgentLab and Proxmox
- missing IP or networking metadata
- current CPU/memory when live VM config is available

## /sandbox-update
Prefer audited resource changes through AgentLab over raw `qm set`.

```bash
agentlab sandbox update --memory 8GiB <vmid>
agentlab sandbox update --cores 4 <vmid>
```

Use this if a sandbox is healthy but underprovisioned for the current task.

## /secrets-validate
Validate host-side access material when SSH or guest Tailscale setup is suspect.

```bash
agentlab secrets show
agentlab --json secrets validate
```

## Restart patterns
- Validate daemon logs first.
- Confirm Proxmox `qm list` and event failures.
- Use host-level status and logs from `agentlab status`.
