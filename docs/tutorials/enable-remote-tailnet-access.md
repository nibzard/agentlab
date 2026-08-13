# Enable remote tailnet access

Goal: reach the daemon and your sandboxes from a workstation over a Tailscale tailnet, then SSH into a sandbox from your laptop.

## Prerequisites

- A daemon host set up through [Set up the Proxmox host](set-up-proxmox-host.md), joined to a Tailscale tailnet.
- Tailscale installed on both the daemon host and your workstation, with both devices authenticated to the same tailnet.
- The `agentlab` CLI installed on your workstation, as described in [Install AgentLab](install-agentlab.md).
- At least one running sandbox, as described in [Create your first sandbox](create-first-sandbox.md).

## Steps

1. On the daemon host, enable the remote control plane. The control listener defaults to TCP port `8845` and requires a bearer token.

    ```bash
    sudo agentlab init --apply --control-port 8845
    ```

   `agentlab init` reports host readiness and, with `--apply`, writes `control_listen` and `control_auth_token` to `/etc/agentlab/config.yaml` (mode `0600`) and restarts `agentlabd`. Add `--rotate-control-token` to generate a fresh token, or `--tailscale-serve` to publish the listener through Tailscale Serve.

   !!! warning
       A wildcard `control_listen` bind (for example `0.0.0.0:8845`) is rejected unless you also set `control_allow_cidrs`. Prefer binding to loopback and publishing through Tailscale Serve.

2. Note the control token the init step printed, and the daemon's Tailscale DNS name. Confirm the daemon answers over the tailnet from the host itself.

    ```bash
    curl -sf http://localhost:8845/healthz
    ```

3. From your workstation, save the remote endpoint, token, and jump host. `connect` normalizes the URL, verifies `/v1/status`, reads `/v1/host` for the jump host, and writes `~/.config/agentlab/client.json` with mode `0600`.

    ```bash
    agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token>
    ```

   The endpoint must include an explicit `http://` or `https://` scheme. Plain HTTP to a non-loopback host is rejected unless you pass `--allow-insecure-http`; this is intended only inside a trusted tunnel such as Tailscale.

4. Confirm the workstation now talks to the remote daemon.

    ```bash
    agentlab status
    ```

5. SSH into a sandbox from the workstation. The CLI fetches the sandbox, probes TCP/22 on the agent subnet, and falls back to SSH ProxyJump through the daemon host when the subnet route is unavailable.

    ```bash
    agentlab ssh <vmid>
    ```

## Expected result

`agentlab status` run from the workstation returns the remote daemon's status snapshot. `agentlab ssh <vmid>` opens a shell in the sandbox without you SSHing to the host first. `agentlab disconnect` removes the saved `client.json` and returns the CLI to local-socket mode.

## Next

For deeper remote-control configuration and ProxyJump troubleshooting, see [Connect to a remote daemon over the tailnet](../how-to/connect-remote-daemon-over-tailnet.md). For the trust model behind the control and guest listeners, see [Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).
