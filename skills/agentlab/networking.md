# Networking

## /sandbox-list
Check network health quickly.

```bash
agentlab sandbox list
agentlab status
```

Look for stale states and missing `ip`/routing evidence before SSH handoff.

## /sandbox-show
Inspect a sandbox before changing connectivity or access assumptions.

```bash
agentlab sandbox show <vmid>
agentlab sandbox show <vmid> --json
```

Use this to confirm:
- current `state`
- current `ip`
- network mode / firewall group
- current `resources` when the backend can read live VM config

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

## /secrets-access-prep
Prepare guest access from the host side before assuming SSH or Tailscale are configured.

```bash
agentlab secrets show
agentlab secrets add-ssh-key --name laptop --key-file ~/.ssh/id_ed25519.pub
agentlab secrets set-tailscale --authkey tskey-auth-XXXX --hostname-template 'agentlab-{vmid}' --extra-arg --ssh
agentlab --json secrets validate
```

Use this when:
- new sandboxes need authorized SSH keys
- guest Tailscale bootstrap should be preconfigured
- an agent needs to verify host-side access material before creating or debugging a sandbox
