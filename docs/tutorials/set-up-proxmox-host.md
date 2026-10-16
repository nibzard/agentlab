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

## Prerequisites for remote template builds

Install the build prerequisites explicitly on the Proxmox node:

```bash
apt-get update
apt-get install -y libguestfs-tools qemu-utils curl ca-certificates jq
```

The existing Proxmox `qm`, `pvesh`, `pvesm`, systemd, Bash, and SHA-256 tools
must be available. The build node must run Linux amd64 with usable `/dev/kvm`,
libguestfs, local disk storage, a configured bridge, and enough space for the
working image and imported disk. `agentlab template capabilities` and
`agentlab template validate` report missing prerequisites without installing
packages or changing networking.
The recipe uses `virt-resize` to expand the Ubuntu root filesystem to the
requested disk size before installing packages and agent tools, then refreshes
GRUB's partition references so the resized image can boot with Proxmox's BIOS.

Workers run as root with root's primary group in separate
`agentlab-template-tb_*.service` transient units. They need KVM/device access,
Proxmox IPC under `/run`, pmxcfs under `/etc/pve`, local storage under
`/var/lib/vz` (including ZFS devices), task logs under `/var/log/pve/tasks`, and
private build/cache data under `/var/lib/agentlab`. The unit sets
`ProtectSystem=strict`, explicit writable paths, `UMask=0077`, a fixed
environment, 8 GiB memory and 512-task limits, a runtime bound, and process-group
cancellation. Custom directory storage outside the writable paths requires a
reviewed host-service policy change before use. Do not disable daemon filesystem
protection globally. Missing path/device access appears in capability errors or
build logs. Verify this policy on a designated test node before production use.

Worker commands set `TMPDIR`, `LIBGUESTFS_TMPDIR`, `LIBGUESTFS_CACHEDIR`, and
`XDG_RUNTIME_DIR` to private directories inside that build's writable state
directory. This keeps libguestfs appliance/cache files and temporary sockets
within the existing write policy; see the [libguestfs environment variables](https://libguestfs.org/guestfs.3.html#environment-variables).
Debian's libguestfs 1.48 still creates short appliance socket paths under `/tmp`,
so the worker unit also permits writes there. This exception applies only to
workers; the API daemon retains its existing filesystem protection.
The worker removes these temporary directories when completion or cleanup succeeds.
