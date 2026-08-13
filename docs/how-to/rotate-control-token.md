# How to rotate the control-plane token

Replace the `control_auth_token` bearer used by the remote control plane without
losing the daemon or its sandboxes.

## Prerequisites

- The remote control plane is enabled (`control_listen` is set). The token is
  mandatory whenever `control_listen` is set, and is treated as high-privilege
  because it can create and destroy VMs.
- Root access on the Proxmox host.
- The remote clients that will need to reconnect after the change.

## Steps

1. Rotate the token on the host with one of the following. Both regenerate the
   token, write `control_listen` (loopback on the default port 8845) and
   `control_auth_token` to `/etc/agentlab/config.yaml`, and force the file to
   `0600`:

    ```bash
    sudo agentlab init --apply --rotate-control-token
    ```

    ```bash
    sudo scripts/install_host.sh --enable-remote-control --rotate-control-token
    ```

   Without `--rotate-control-token`, both commands reuse the existing token.

2. Both commands restart `agentlabd` after they write the config. Restart the
   daemon by hand only if you edited `/etc/agentlab/config.yaml` directly:

    ```bash
    sudo systemctl restart agentlabd.service
    ```

3. Reconnect each remote client with the new token. The endpoint is plain HTTP
   over the tailnet, so allow insecure HTTP before you connect:

    ```bash
    export AGENTLAB_ALLOW_INSECURE_HTTP=1
    agentlab connect --endpoint http://host.tailnet.ts.net:8845 --token <new-token>
    ```

## Verify

- `agentlab status` returns the status snapshot, which proves the new token
  authenticates.
- Clients that still hold the old token now fail with an auth error and must
  rerun `agentlab connect`.

!!! tip "Rotate on exposure"
    Rotate the token if it leaks, if a laptop is lost, or after offboarding a
    user. Prefer tailnet-only access and never bind `control_listen` to a
    wildcard address without `control_allow_cidrs`. See
    [Control plane and trust boundaries](../explanation/control-plane-and-trust-boundaries.md)
    and the [Configuration reference](../reference/configuration.md).
