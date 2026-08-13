# How to configure the Proxmox API backend

Switch `agentlabd` from the default shell backend to the recommended API
backend. The API backend talks to the Proxmox REST API with a dedicated API
token instead of shelling out to `qm`, `pvesh`, and `pvesm`.

For the trade-offs between the two backends, see
[Shell versus API backend](../explanation/shell-vs-api-backend.md). For the
full key list, see [Configuration reference](../reference/configuration.md).

## Prerequisites

- A Proxmox VE host on version 8.x or newer. No specific version is required
  for either backend; version 9.x is recommended.
- `root` access to the Proxmox node, or a user that can create API tokens.
- A working `agentlabd` install and the agent bridge `vmbr1` already configured.

## Steps

1. Create a dedicated, privilege-separated API token on the Proxmox node.

    ```bash
    pveum user token add agentlab@pve agentlab --privsep 0
    ```

    The token value has the form `user@realm!tokenid=uuid`. Copy the full
    string; the daemon needs it.

2. Edit `/etc/agentlab/config.yaml` (mode `0600`) and select the API backend.

    ```yaml
    proxmox_backend: api
    proxmox_api_url: https://proxmox.example:8006
    proxmox_api_token: agentlab@pve!agentlab=<uuid>
    proxmox_node: proxmox            # optional; auto-detected when empty
    ```

    !!! note "Default backend is shell"
        `proxmox_backend` defaults to `shell` in the code, not `api`. You must
        set it to `api` explicitly to switch.

3. Choose a TLS verification mode. Leave verification on unless you use
   self-signed certificates that you cannot install as a CA.

    ```yaml
    proxmox_tls_ca_path: /etc/agentlab/proxmox-ca.pem   # recommended
    ```

    You cannot combine `proxmox_tls_insecure: true` with `proxmox_tls_ca_path`.
    The daemon rejects that combination at config load.

4. Restart the daemon so it picks up the new backend.

    ```bash
    agentlabd -config /etc/agentlab/config.yaml
    ```

    If you run the host under systemd:

    ```bash
    sudo systemctl restart agentlabd.service
    ```

## Verify

Confirm the daemon is healthy and the backend can reach Proxmox.

```bash
agentlab status
```

Then create a throwaway sandbox to exercise the clone path end to end.

```bash
agentlab sandbox new --profile yolo-ephemeral --name backend-smoke
agentlab sandbox show <vmid>
agentlab sandbox destroy --force <vmid>
```

`sandbox new` returns the numeric VMID. `sandbox show` and `sandbox destroy`
take that VMID, not the sandbox name. A `RUNNING` sandbox confirms the API
backend cloned the template, resized the
disk, and discovered the guest IP. For token-permission errors, check that the
token was created with `--privsep 0` and that the user has the `VM.Allocate`,
`VM.Audit`, and `Datastore.Allocate` privileges on the target pool.
