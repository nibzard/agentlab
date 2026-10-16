# Build a VM template

Goal: create a cloneable cloud-init VM template on Proxmox and register a profile so the daemon can clone sandboxes from it.

## Build remotely with AgentLab

Connect using your saved endpoint and token (`agentlab connect`). The laptop
needs only the AgentLab CLI; image download, customization and import run on
the local Proxmox daemon host. Install the explicit host prerequisites in
[Set up the Proxmox host](set-up-proxmox-host.md#prerequisites-for-remote-template-builds).

```bash
agentlab template capabilities
agentlab template validate --name ubuntu-2404-agentlab --storage local-zfs
agentlab template create --name ubuntu-2404-agentlab --storage local-zfs --wait
agentlab template build list
agentlab template build show tb_example
agentlab template build logs tb_example --follow
agentlab template build wait tb_example --timeout 45m
```

Use the actual build ID printed by create. Disconnecting does not cancel the
build. Ctrl-C prints a reconnect command. For a lost submission response, retry
with the printed `--request-id` and exactly the same inputs. To stop work, use
`agentlab template build cancel BUILD_ID`. A retry after failure is a new build
with a fresh VMID. Existing resources are never overwritten.

The build reserves a second unused VMID for a disposable smoke clone. It boots
that clone and verifies guest-agent access, networking, SSH, the runner and
unenrolled Tailscale before reporting success. The clone receives no credentials
and is removed after verification. `template show` includes `boot_verified` and
its timestamp. Historical manifests without this evidence must be rebuilt
before activation.

Inspect and activate a successful managed template explicitly:

```bash
agentlab template list
agentlab template show 9010
agentlab template activate 9010 --profile ubuntu-24-04-ai --expected-template-vmid 9999
```

Replace those IDs and profile with your actual values. The target profile must
already exist. Activation checks the old VMID and template drift, preserves the
other YAML fields/documents, and reloads future sandbox requests without a
restart. Existing sandboxes retain their original disks/template.

The manifest reports installed Tailscale, runner, SSH, guest agent and optional
pinned CLI versions. Base templates have no Tailscale identity or enrollment
state. The [sandbox bootstrap](../specs/standalone-sandbox-tailscale-bootstrap.md)
attempts enrollment for fresh standalone guests when a valid credential is
configured. Check `sandbox show` for the independent Tailscale state and
bootstrap diagnostics; installation alone is not evidence of connectivity. Test a dedicated sandbox for DHCP, SSH, guest-agent access,
runner availability and `tailscale version` before activating production profiles.

An unacknowledged worker launch may remain in preflight for up to one minute
while the daemon checks whether systemd accepted it.

If a worker disappears during a daemon restart, the daemon marks an uncertain
result `template_build_interrupted` and never reruns import blindly. Inspect
`cleanup_required`, recorded resources and the worker unit with `systemctl
status`. Remove only resources with the matching build ownership tag after
Proxmox tasks settle; retain unrelated VMs. Submit a fresh build to retry.

## Manual host workflow

The existing `scripts/create_template.sh` interface remains available to host
operators. The steps below are also useful for understanding the underlying
Proxmox operations. Templates made manually are unmanaged and cannot be activated
through the managed-template API in this release.

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
