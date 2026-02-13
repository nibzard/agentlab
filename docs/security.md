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

## Threat model notes

- Treat the admin API key or OAuth client secret as high-value secrets.
- Use least-privilege credentials and rotate them after bootstrap or onboarding.
- Disable or delete unused API keys once the route is approved.
