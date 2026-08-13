# How to connect to a remote daemon over the tailnet

Point the `agentlab` CLI at a daemon that runs on another host, then drive it
and SSH into sandboxes from your workstation.

## Prerequisites

- The remote control plane is enabled on the host. Either topology is valid:
  - **Topology A (recommended):** `control_listen: 127.0.0.1:8845` published
    with `tailscale serve`.
  - **Topology B:** `control_listen` bound to the host tailnet IP, with
    `control_allow_cidrs` set to your tailnet range.
- `control_auth_token` is set. It is mandatory whenever `control_listen` is set,
  and wildcard binds are rejected without `control_allow_cidrs`.
- The `agentlab` CLI installed on your workstation, on the same tailnet.

## Steps

1. Save the endpoint, token, and (optional) jump host. The endpoint must include
   an explicit `http://` or `https://` scheme:

    ```bash
    agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token>
    ```

   `connect` verifies `GET /v1/status` as a hard check (failure aborts), then
   makes a best-effort `GET /v1/host` whose failure is ignored. It then writes
   `~/.config/agentlab/client.json` with mode `0600`.

2. Confirm the connection:

    ```bash
    agentlab status
    ```

3. Remove the saved connection when you no longer need it:

    ```bash
    agentlab disconnect
    ```

## SSH into a sandbox remotely

`agentlab ssh` probes TCP/22 on the sandbox IP over the tailnet subnet route. If
the route is unreachable and a jump host is configured, it falls back to SSH
ProxyJump automatically.

```bash
agentlab ssh <vmid>
agentlab ssh <vmid> --jump-host host.tailnet.ts.net --jump-user <user>
```

Save jump defaults once with `connect` so later `agentlab ssh` calls use them:

```bash
agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <token> \
  --jump-host host.tailnet.ts.net --jump-user <user>
```

## Verify

- `agentlab status` returns the status snapshot from the remote daemon.
- `agentlab ssh <vmid>` resolves to a direct or ProxyJump SSH command.

!!! warning "Plaintext HTTP needs an explicit opt-in"
    Plaintext HTTP to a non-loopback host is rejected unless you pass
    `--allow-insecure-http` or set `AGENTLAB_ALLOW_INSECURE_HTTP`, and only
    inside a trusted tunnel such as Tailscale. Precedence, highest to lowest, is
    CLI flags, then `AGENTLAB_ENDPOINT` / `AGENTLAB_TOKEN`, then `client.json`.

For a copy-paste quickstart that includes `agentlab bootstrap`, see
[Enable remote tailnet access](../tutorials/enable-remote-tailnet-access.md).
For the trust model behind the control listener, see
[Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md).
