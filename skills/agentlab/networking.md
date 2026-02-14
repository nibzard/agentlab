# Networking

## /sandbox-list
Check network health quickly.

```bash
agentlab sandbox list
agentlab status
```

Look for stale states and missing `ip`/routing evidence before SSH handoff.

## /logs
Stream events when investigating exposure or routing issues.

```bash
agentlab logs <vmid> --tail 200
agentlab logs <vmid> --follow
```

## /ssh
Open interactive SSH to a sandbox.

```bash
agentlab ssh <vmid>
agentlab ssh <vmid> --user ubuntu --exec "tailscale status"
```

### Connectivity notes
- `agentlab` may prefer direct subnet access when route is approved.
- If subnet access is unavailable, add `--jump-host`/`--jump-user` workflow in `agentlab connect` context.
