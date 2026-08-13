# How to run AgentLab air-gapped

Operate the daemon on a host with no outbound internet.

## Prerequisites

- All VM templates already present on the Proxmox node, and (for the Docker
  backend) all container images pre-pulled. The daemon does not download images
  in offline mode.
- A local mirror or private address for any service sandboxes must reach.

## Steps

1. Enable offline mode with the flag or the config file. The flag overrides the
   config:

    ```bash
    agentlabd --offline
    agentlabd --config /etc/agentlab/config.yaml --offline
    ```

    ```yaml
    # /etc/agentlab/config.yaml
    offline: true
    ```

2. If you need TLS and subdomain routing, use a self-signed proxy. Let's Encrypt
   is rejected offline because ACME needs internet:

    ```yaml
    proxy_enabled: true
    proxy_domain: agentlab.local
    proxy_tls_mode: self-signed
    ```

3. Restart the daemon if you changed the config, then confirm:

    ```bash
    sudo systemctl restart agentlabd.service
    agentlab status
    ```

## What offline mode changes

- Outbound HTTP to public addresses is blocked. Only loopback, link-local,
  RFC-1918, and `fc00::/7` destinations are allowed.
- The Tailscale exposure publisher is disabled. Only Tailscale exposure is
  unavailable offline; the Caddy self-signed proxy (`proxy_enabled`) still
  exposes ports. See [expose a sandbox port](expose-a-sandbox-port.md).
- `proxy_tls_mode: letsencrypt` fails validation; use `self-signed`.
- The metadata endpoint at `169.254.169.254` works fully offline because it is a
  link-local address.

## Verify

- `agentlab status` has no offline indicator. Confirm offline mode from the
  daemon startup log instead.
- A new sandbox boots and reaches `RUNNING` from the local template.
- The daemon log prints `offline mode enabled: all external network calls are
  blocked`.

!!! note "Integrations are not yet documented"
    The integration credential proxy can forward sandboxes to private upstreams
    such as a local LLM mirror, but provider setup and credential shapes are not
    yet documented. Behavior here may change.

For the full list of affected config keys and validation rules, see the
[Configuration reference](../reference/configuration.md) and
[agentlabd flags](../reference/agentlabd-flags.md).
