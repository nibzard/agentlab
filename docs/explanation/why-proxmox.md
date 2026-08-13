# Why Proxmox

AgentLab runs each AI coding agent inside its own Proxmox VE virtual machine.
This page explains why AgentLab chose full VM isolation on Proxmox over the
obvious alternatives, and what trade-offs that choice brings.

## The isolation requirement

Coding agents in "dangerous" or YOLO mode run arbitrary commands, install
packages, and rewrite files. The host that runs them must assume the agent, and
the repository the agent clones, are untrusted. Two failure modes matter most: a
compromised agent reaching the host or the local network, and one sandbox
poisoning another.

AgentLab answers both with hardware-enforced isolation. Each agent gets a
dedicated VM with its own kernel, its own ephemeral root disk, and a network
position that allows general outbound Internet while blocking every private
range. The isolation model is detailed in
[Network isolation model](network-isolation-model.md).

## VMs over containers

Containers share the host kernel. A kernel exploit or a misconfigured mount
inside one container can reach the host and every neighbor. Linux namespaces and
seccomp reduce this risk, but they do not remove it. For a workload that
deliberately runs untrusted code, the defense-in-depth value of a separate
kernel is the deciding factor.

VMs also give each sandbox a clean, disposable root filesystem. Destroying or
reverting a sandbox resets the root disk completely, which is the property the
state machine relies on. Persistent state lives on a separate workspace volume
that can be detached and reattached, as described in
[Workspaces, sessions, and sandboxes](workspaces-sessions-sandboxes.md).

## Proxmox over libvirt or a public cloud

The project evaluated libvirt and direct `virsh` automation before settling on
Proxmox VE. Proxmox ships the pieces AgentLab needs as integrated, scriptable
primitives:

- `qm` for clone, start, stop, destroy, and snapshot operations.
- `pvesh` for JSON status and configuration queries.
- `pvesm` for volume allocation and ZFS snapshots.
- Native cloud-init snippet support through `cicustom`.
- `qemu-guest-agent` for reliable guest IP discovery.
- Linked cloning from a template for fast, copy-on-write provisioning.

Linked clones let a sandbox boot from a clean template in roughly 30 seconds
without copying the full disk. The measured provisioning time, from the
`agentlab sandbox new` request to an SSH-ready VM, is on the order of 30
seconds. The shell backend works on Proxmox VE 8.x and later; the API backend
requires Proxmox VE 9.x or later.

A public cloud would shift the trust boundary to the cloud provider and make
air-gapped operation impossible. AgentLab is designed for operators who
self-host a Proxmox cluster, including fully offline deployments. See
[Run AgentLab air-gapped](../how-to/run-air-gapped-offline.md).

## The two-backend trade-off

Proxmox exposes two control surfaces, and AgentLab implements both behind one
`Backend` interface. The shell backend shells out to `qm`, `pvesh`, and `pvesm`.
The API backend uses the Proxmox REST API with a dedicated, least-privilege
token.

The shell backend is the default, because it works with no extra credentials and
covers every operation including ZFS volume snapshots and clones. The API
backend is recommended for new deployments because it avoids the IPC constraints
of `qm` under systemd and authenticates with a scoped token instead of root
shell access. The trade-offs are explored in
[Shell versus API backend](shell-vs-api-backend.md), and the operational switch
is covered in [Configure the Proxmox API backend](../how-to/configure-proxmox-api-backend.md).

## What Proxmox does not solve

Proxmox gives isolation and fast cloning, not multi-tenant identity or a managed
network. AgentLab adds its own control-plane authentication, per-sandbox egress
filtering, and one-time secret delivery on top. Those layers are the subject of
[Control plane and trust boundaries](control-plane-and-trust-boundaries.md) and
[Secrets delivery model](secrets-delivery-model.md). Workspace snapshot, fork,
and restore depend on ZFS-backed storage, and behavior on other storage backends
is not yet documented.
