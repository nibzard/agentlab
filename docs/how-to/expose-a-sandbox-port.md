# How to expose a sandbox port on the tailnet

Publish a sandbox TCP port to your tailnet with host-owned Tailscale Serve so a
workstation can reach a service inside the sandbox.

## Prerequisites

- The `tailscale` CLI is installed and logged in on the Proxmox host, and
  MagicDNS is enabled in your tailnet.
- The daemon is not in offline mode. Offline mode disables the Tailscale
  exposure publisher because it needs internet coordination. See
  [Run AgentLab air-gapped](run-air-gapped-offline.md).
- The sandbox is running and has an IP on the agent subnet.

## Steps

1. Expose the port. The port is given as `:<port>`:

    ```bash
    agentlab sandbox expose 1009 :8080
    ```

   The command asks for confirmation. Add `--force` to skip the prompt:

    ```bash
    agentlab sandbox expose 1009 :8080 --force
    ```

2. List active exposures:

    ```bash
    agentlab sandbox exposed
    ```

3. Remove an exposure by name. Names default to `sbx-<vmid>-<port>`:

    ```bash
    agentlab sandbox unexpose sbx-1009-8080
    ```

## Verify

- `agentlab sandbox exposed` lists the exposure with its URL and state.
- From a tailnet device, connect to the raw TCP URL the command prints:
  `tcp://<tailnet-magic-dns-name>:<port>`. Use your tailnet's real MagicDNS
  name. The exposure is raw TCP, not HTTPS.

!!! tip "Exposures are audited and cleaned up"
    The daemon emits an audit event for every expose and unexpose, and removes
    an exposure (best-effort) when its owning sandbox is destroyed. Remove stale
    rules manually with `tailscale serve --tcp=<port> off` if one remains.

For the route and request shapes (`POST /v1/exposures`, `GET /v1/exposures`,
`DELETE /v1/exposures/{name}`), see the
[HTTP API reference](../reference/http-api.md). For the trust model of the
control plane that authorizes these calls, see
[Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).
