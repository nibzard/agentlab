# Profile and template schema

Profiles select the resources, network mode, and behavior for a sandbox.
Templates are cloneable Proxmox VMs that vm-type profiles reference. This page
lists the fields, value constraints, and validation rules. For authoring
steps, see ../how-to/author-a-profile.md. For template building, see
../tutorials/build-a-vm-template.md.

## Profile required fields

Profiles are YAML files in `profiles_dir` (default `/etc/agentlab/profiles`).
A file may contain multiple documents separated by `---`. The daemon parses
these fields strictly at load time (`internal/daemon/profiles.go`):

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | string | yes | Unique across all profiles; duplicates are an error. |
| `template_vmid` | int | yes (vm only) | Must be greater than 0. |
| `type` | string | no | `vm` (default) or `lxc`. Other values are rejected. |
| `image` | string | yes (lxc only) | Container image, for example `ubuntu:22.04`. |

All other fields are stored as raw YAML and interpreted at provisioning time.

## Profile network fields

| Field | Under | Description |
| --- | --- | --- |
| `bridge` | `network` | Proxmox bridge, for example `vmbr1`. |
| `model` | `network` | Network device model, for example `virtio`. |
| `mode` | `network` | `off`, `nat` (default), or `allowlist`. |
| `firewall` | `network` | Boolean; enables the Proxmox NIC firewall flag. |
| `firewall_group` | `network` | Explicit firewall group. |
| `ip_config0` | `network` | Cloud-init `ipconfig0` line for net0, for example `ip=10.77.0.20/24,gw=10.77.0.1`. Literal IPv4 and IPv6 addresses are reserved before cloning; another sandbox cannot use the same address until its owner is destroyed, including across daemon restarts. Use different fixed addresses for concurrent sandboxes. `ip=dhcp` and `ip6=auto` do not reserve addresses. |
| `nameserver` | `network` | Cloud-init nameserver, for example `10.77.0.1`. |

`network.mode` maps to a Proxmox firewall group:

| Mode | Firewall group |
| --- | --- |
| `off` | `agent_nat_off` |
| `nat` | `agent_nat_default` |
| `allowlist` | `agent_nat_allowlist` |

Setting `network.firewall: false` together with a resolved firewall group or
mode is a validation error.

## Profile resources fields

| Field | Description |
| --- | --- |
| `cores` | vCPU count. |
| `memory_mb` | Memory in MB. |
| `balloon_mb` | Virtio balloon minimum target in MB. The guest still sees `memory_mb`; the host may reclaim down to `balloon_mb`. |
| `cpulist` | CPU pinning (`cpulist` in Proxmox). |
| `cpu_over_commit` | CPU limit override. |
| `mem_over_commit` | Memory over-commit factor. |
| `allow_burst` | Allow bursting. |

## Profile storage fields

| Field | Description |
| --- | --- |
| `root_size_gb` | Root disk size in GB. |
| `scsihw` | SCSI hardware. Defaults to `virtio-scsi-pci` when unset, because cloud images lack LSI drivers in the initramfs. |

## Profile behavior fields

| Field | Description |
| --- | --- |
| `keepalive_default` | Default keepalive flag for new sandboxes. |
| `ttl_minutes_default` | Default lease TTL in minutes. Ignored when less than or equal to 0. |
| `idle_stop_minutes_default` | Per-profile idle-stop override. Set to 0 to disable idle stop for this profile. |
| `inner_sandbox` | Inner containment. Only `bubblewrap` is supported. See ../how-to/use-the-inner-bubblewrap-sandbox.md. |
| `inner_sandbox_args` | Extra bubblewrap arguments, appended token-by-token. |

## Other profile fields

The shipped default profiles also pass these fields through to the guest:

| Field | Description |
| --- | --- |
| `secrets_bundle` | Bundle name delivered to the sandbox. |
| `repo.clone_path` | Where the job repo is cloned in the guest (`/work/repo` or `/tmp/repo`). |
| `artifacts.upload` | Whether the guest uploads artifacts. |
| `artifacts.endpoint` | Artifact upload URL. |

## Profile validation rules

Enforced at provisioning time (`internal/daemon/profile_validation.go`):

- Host mounts are rejected. Detected keys include `host_mount`, `bind_mount`,
  `virtiofs`, and any key matching host+(mount/path/bind) or bind+mount. Use
  workspace disks instead.
- `behavior.inner_sandbox`, when set, must be `bubblewrap` or a recognized
  enabled alias (`bwrap`, `true`, `yes`, `1`). The values `none`, `off`,
  `false`, `0`, and `disabled` disable it.
- `network.mode`, when set, must be one of `off`, `nat`, `allowlist`.
- LXC profiles require `image`; vm profiles require `template_vmid`.

## Template schema

Templates are described by the standalone `cmd/template` helper. The helper
prints a prompt for generating template YAML at
`/etc/agentlab/templates/<name>.yml`. It is a standalone binary and is not
wired into the `agentlab` CLI.

The template value constraints the helper emits are:

| Field | Constraint |
| --- | --- |
| `proxmox.memory_mb` | Power of two, 2048 to 16384. |
| `proxmox.cores` | 1 to 8. |
| `proxmox.storage.disk_size_gb` | 20 to 200. |
| `proxmox.vmid` | Greater than or equal to 9000 (auto-assigned from the 9xxx range). |
| `image.url` | Cloud image URL. Default Ubuntu 24.04 LTS noble. |

!!! note
    The values above are guidance emitted by `cmd/template`. The template rules
    the daemon enforces at provisioning are qemu-guest-agent enablement and a
    configured cloud-init drive.

## Template validation enforced by the daemon

`ValidateTemplate` returns success only when the template VM exists, has the
qemu-guest-agent enabled, and has a cloud-init drive configured. A missing
guest agent produces:

```text
template VM <vmid> does not have qemu-guest-agent enabled (missing 'agent:' config)
```

Enable the agent with `proxmox.agent: 1`, set
`cloud_init.install_qemu_guest_agent: true`, and install it through
`apt-get install -y qemu-guest-agent` in `runcmd`.

## Default profiles shipped

`scripts/profiles/defaults.yaml` ships the profiles below. Copy each document
into its own file under `profiles_dir`.

| Profile | Type | Workspace | Default TTL |
| --- | --- | --- | --- |
| `yolo-ephemeral` | vm | none | 180m |
| `yolo-workspace` | vm | attach | 480m |
| `interactive-dev` | vm | none | 720m |
| `agent-base` | lxc | - | 480m |
| `agent-claude` | lxc | - | 480m |
| `agent-codex` | lxc | - | 480m |

Backend selection belongs in the daemon's `backend` configuration. Profiles
accept only `vm` and `lxc` types; `sandbox new --type docker` and a client
`default-backend` preference are unsupported. See [CLI reference](cli.md) and
[daemon configuration](configuration.md).
