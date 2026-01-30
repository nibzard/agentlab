# agentlab

## Overview

AgentLab provisions unattended, network-isolated VM sandboxes on Proxmox VE for
running AI coding agents in "dangerous"/YOLO mode.

Key components:
- `agentlabd` daemon on the Proxmox host (owns Proxmox access, enforces policy).
- `agentlab` CLI for local control via a Unix socket.
- Guest `agent-runner` service inside the VM template for bootstrap + execution.

Security posture by default:
- Full outbound Internet access with RFC1918/ULA egress blocks.
- No host bind mounts; optional persistent workspaces via separate disks.
- One-time secrets delivery into tmpfs only.

## Prerequisites

- Proxmox VE 8.x host with `qm`/`pvesh` available.
- Storage pool suitable for templates/clones (ZFS or LVM-thin recommended).
- `vmbr0` for LAN/WAN and ability to create `vmbr1` for the agent subnet
  (defaults to `10.77.0.0/16`).
- Go toolchain (see `go.mod`) to build the binaries.
- Tailscale on the host for remote SSH access (recommended).

## Quickstart (host setup)

1) Build binaries:

```bash
make build
```

2) Install binaries + systemd unit:

```bash
sudo scripts/install_host.sh
```

3) Configure networking (agent bridge + NAT/egress blocks):

```bash
sudo scripts/net/setup_vmbr1.sh --apply
sudo scripts/net/apply.sh --apply
```

4) (Recommended) Enable Tailscale subnet routing:

```bash
sudo scripts/net/setup_tailscale_router.sh --apply
```

5) Create secrets and minimal config/profile, then build the template:

```bash
sudo scripts/create_template.sh
sudo systemctl restart agentlabd.service
```

6) Run a job from the host:

```bash
agentlab job run --repo <git-url> --task "<task>" --profile yolo-ephemeral
```

For full operator setup, see the runbook below.

## Documentation

- Runbook: `docs/runbook.md`
- Secrets bundles: `docs/secrets.md`
- Local control API: `docs/api.md`
