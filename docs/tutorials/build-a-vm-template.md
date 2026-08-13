# Build a VM template

Goal: create a cloneable cloud-init VM template on Proxmox and register a profile so the daemon can clone sandboxes from it.

## Prerequisites

- A Proxmox host set up as described in [Set up the Proxmox host](set-up-proxmox-host.md), including the `vmbr1` bridge and DHCP.
- Root access to the Proxmox host.
- A ZFS storage pool such as `local-zfs`. Linked clones and workspace snapshots require ZFS.

## Steps

1. Download a cloud image to the Proxmox ISO directory. The default image used by the bundled profiles is Ubuntu 24.04 LTS.

    ```bash
    wget -O /var/lib/vz/template/iso/noble-server-cloudimg-amd64.img \
      https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img
    ```

2. Create the template VM from the image. Use VMID `9000`, which the bundled profiles reference, and enable the QEMU guest agent. The daemon rejects templates that do not have the guest agent enabled.

    ```bash
    qm create 9000 \
      --name agentlab-template \
      --memory 2048 \
      --cores 2 \
      --net0 virtio,bridge=vmbr1 \
      --scsihw virtio-scsi-pci \
      --scsi0 local-zfs:0,import-from=/var/lib/vz/template/iso/noble-server-cloudimg-amd64.img \
      --agent enabled=1
    ```

3. Install the QEMU guest agent inside the image with `virt-customize`. The tool only installs the agent; it does not configure DHCP. DHCP on the sandbox interface is configured separately through cloud-init. For example:

    ```bash
    virt-customize --format=raw --network \
      -a /dev/zvol/rpool/data/vm-9000-disk-0 \
      --install qemu-guest-agent
    ```

4. Convert the VM into a template. After this step Proxmox can clone it.

    ```bash
    qm template 9000
    ```

5. Author a profile that references the template. Copy the bundled `yolo-ephemeral` profile into `/etc/agentlab/profiles/` as its own file. The only required fields are `name` and `template_vmid`.

    ```yaml
    name: yolo-ephemeral
    template_vmid: 9000
    network:
      bridge: vmbr1
      model: virtio
      mode: nat
      firewall_group: agent_nat_default
    resources:
      cores: 4
      memory_mb: 6144
    storage:
      root_size_gb: 40
      workspace: none
    ```

   !!! note
       Profiles that request host bind mounts (`host_mount`, `bind_mount`, `virtiofs`) are rejected at provisioning time. Persistent state uses a separate workspace disk, not a host mount.

6. Reload the daemon so it picks up the new profile, then confirm the profile and template are visible.

    ```bash
    sudo systemctl restart agentlabd
    agentlab profile list
    ```

## Expected result

`qm list` shows VMID `9000` as a template. `agentlab profile list` lists `yolo-ephemeral` with its `template_vmid`. A sandbox created from the profile boots, receives a `10.77.0.0/16` address from DHCP, and reports `RUNNING`.

## Next

Provision a sandbox from your new template in [Create your first sandbox](create-first-sandbox.md). For the full set of profile fields and value constraints, see [Profile and template schema](../reference/profile-and-template-schema.md).
