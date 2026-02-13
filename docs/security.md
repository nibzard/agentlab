# Security notes

## Tailscale Admin API (optional)

AgentLab can optionally call the Tailscale Admin API during `agentlab bootstrap` to
approve the agent subnet route automatically. This is strictly opt-in because the
credentials grant tailnet-wide control.

Use one of these client-side credential sources:
- Environment variables (`AGENTLAB_TAILSCALE_API_KEY` or OAuth client env vars).
- `~/.config/agentlab/client.json` under the `tailscale_admin` key (file is forced to `0600`).

Never place tailnet admin credentials in `/etc/agentlab` on the host or any file that is
readable by other users.

## Host config permissions

`/etc/agentlab/config.yaml` is treated as sensitive. `agentlabd` enforces strict
permissions on startup:
- Require owner-readable and not accessible by others.
- Fail if group-writable/executable or world-readable.
- Warn if group-readable (for example `0640`); prefer `0600`.

## Cloud-init snippet visibility

Cloud-init user-data snippets are stored in the Proxmox snippets storage (default
`/var/lib/vz/snippets`) and are visible in the Proxmox UI and API to anyone who
can view VM config or snippets. These snippets include the one-time bootstrap
token, controller URL, and VMID. The token is short-lived and single-use, but
the snippet content should still be treated as sensitive.

Restrict Proxmox UI access and snippets storage permissions to trusted operators.
Snippets are deleted when sandboxes are destroyed; if a VM is kept or snapshotted,
remove stale snippets manually.

## Guest endpoint rate limiting

Guest-facing endpoints (`/v1/bootstrap/fetch` and `/upload`) are rate limited per IP
to reduce abuse from compromised or misbehaving sandboxes. Limits are configurable in
`/etc/agentlab/config.yaml`. Set the QPS or burst values to `0` only in trusted
environments where rate limiting is not needed.

## Threat model notes

- Treat the admin API key or OAuth client secret as high-value secrets.
- Use least-privilege credentials and rotate them after bootstrap or onboarding.
- Disable or delete unused API keys once the route is approved.
