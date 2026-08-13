# Set up the Proxmox host

Goal: prepare a Proxmox VE host so the daemon can clone VMs onto an isolated bridge, give them addresses, and reach them on the agent subnet.

## Prerequisites

- Proxmox VE 8.x or later for the shell backend, or 9.x or later for the API backend.
- Root access to the Proxmox host.
- The `agentlabd` binary installed, as described in [Install AgentLab](install-agentlab.md).
- A storage pool named `local-zfs` (the default for workspace and snapshot operations). Volume snapshot, restore, and clone only work on ZFS-backed storage.

## Steps

1. Create the isolated bridge `vmbr1` and assign it the host gateway address `10.77.0.1/16` on the agent subnet `10.77.0.0/16`. The daemon's guest listeners default to `10.77.0.1:8844` (bootstrap) and `10.77.0.1:8846` (artifact upload).

2. Install and configure a DHCP server on `vmbr1` so cloned VMs receive addresses. The example uses `dnsmasq`:

    ```bash
    apt-get install -y dnsmasq
    ```

   Create `/etc/dnsmasq.d/agentlab.conf` with the interface, DHCP range, and gateway, then restart `dnsmasq`.

3. Apply the egress filter. AgentLab gives sandboxes full outbound Internet while blocking RFC1918 (`10/8`, `172.16/12`, `192.168/16`) and IPv6 ULA and link-local egress so sandboxes cannot reach the host LAN. The filter is enforced with `nftables` on `vmbr1`.

4. Create the three Proxmox firewall groups that profiles map to through `network.mode`:

   - `agent_nat_off` for mode `off`
   - `agent_nat_default` for mode `nat` (the default)
   - `agent_nat_allowlist` for mode `allowlist`

5. Generate an SSH key pair the daemon injects into sandboxes.

    ```bash
    sudo mkdir -p /etc/agentlab/keys
    sudo ssh-keygen -t ed25519 -f /etc/agentlab/keys/agentlab_id_ed25519 -N ""
    ```

6. Write a minimal `/etc/agentlab/config.yaml` that points at the bridge gateway and the SSH public key:

    ```yaml
    ssh_public_key_path: /etc/agentlab/keys/agentlab_id_ed25519.pub
    secrets_bundle: default
    bootstrap_listen: 10.77.0.1:8844
    artifact_listen: 10.77.0.1:8846
    controller_url: http://10.77.0.1:8844
    ```

   !!! warning
       `/etc/agentlab/config.yaml` must be owner-readable and not group-writable or world-readable. Prefer mode `0600`. The daemon refuses to start otherwise.

7. Install a systemd unit for the daemon. Comment out `NoNewPrivileges` and `PrivateTmp`; the `qm` IPC path breaks if those hardening directives are set, with `ipcc_send_rec: Unknown error -1`.

8. Enable and start the daemon.

    ```bash
    sudo systemctl daemon-reload
    sudo systemctl enable --now agentlabd
    ```

## Expected result

`systemctl status agentlabd` reports the service active. The socket exists at `/run/agentlab/agentlabd.sock`, and `agentlab status` returns a control-plane snapshot.

```bash
ls -la /run/agentlab/agentlabd.sock
agentlab status
```

## Next

The host is ready for a cloneable template. Continue to [Build a VM template](build-a-vm-template.md), then [Create your first sandbox](create-first-sandbox.md). For network isolation details, see [Network isolation model](../explanation/network-isolation-model.md), and for backend choice see [Shell versus API backend](../explanation/shell-vs-api-backend.md).
