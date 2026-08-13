# Architecture

AgentLab provisions unattended, network-isolated Proxmox VE virtual machines so
that AI coding agents can run in "dangerous" mode without threatening the host
or the surrounding network. The design funnels every privileged operation
through one long-running daemon, and treats the agent VMs themselves as
untrusted. This page explains the components, how they are wired, and the path a
single request takes through the system.

## Four binaries, one trust root

AgentLab ships as four binaries built under `cmd/`:

- **agentlabd** is the control-plane daemon. It owns all Proxmox and policy
  access. Nothing else is allowed to call `qm`, `pvesh`, or `pvesm` directly.
- **agentlab** is the user-facing CLI and the only supported surface for users,
  automation, and the Claude Code Skill bundle. It speaks to the daemon over a
  Unix socket or a remote HTTP endpoint.
- **agentlab-dashboard** is an optional web UI that proxies the browser to the
  daemon over that same Unix socket.
- **agentlab-ssh-gateway** is built behind the `sshgateway` build tag and is
  excluded from releases.

The Go module path is `github.com/agentlab/agentlab`, while the user-facing repo
and release target is `nibzard/agentlab`. The two are intentionally different.

## Listeners and bind order

At startup `agentlabd` validates its config, loads profiles from
`/etc/agentlab/profiles`, opens the SQLite database at
`/var/lib/agentlab/agentlab.db`, and binds its listeners in this order:

1. The Unix control socket at `/run/agentlab/agentlabd.sock`.
2. The guest bootstrap listener at `10.77.0.1:8844`.
3. The artifact upload listener at `10.77.0.1:8846`.
4. The optional remote TCP control listener (off by default).
5. The metrics listener (off by default; loopback only when set).

The Unix socket is the trusted, full-access path. The guest listeners bind to
the agent subnet only, and the TCP control plane is gated by a bearer token and
an optional CIDR allowlist. See
[Control plane and trust boundaries](control-plane-and-trust-boundaries.md) for
the trust model behind each listener.

## Managers and the Proxmox backend

The daemon wires a set of managers around a single Proxmox `Backend` interface:

- **SandboxManager** drives the sandbox state machine.
- **JobOrchestrator** provisions sandboxes for jobs and mints bootstrap tokens.
- **WorkspaceManager** owns persistent workspace volumes.
- **ControlAPI**, **SecretsAPI**, **UserAPI**, **IntegrationAPI**, and
  **PoolAPI** expose the versioned `/v1` HTTP surface.
- **IdleStopper**, **ArtifactGC**, and **pool.Pool** run background policy.

The `Backend` interface has two implementations. The shell backend drives the
`qm`, `pvesh`, and `pvesm` CLIs. The API backend talks to the Proxmox REST API
with a dedicated token. The default is the shell backend; the API backend is the
recommended choice. Both are covered in [Shell versus API backend](shell-vs-api-backend.md)
and [Why Proxmox](why-proxmox.md).

## Request and execution flow

A sandbox request travels a short, well-defined path. The CLI sends the request
to the Unix socket; the control plane dispatches it to the SandboxManager; the
manager clones a template, writes a cloud-init snippet, applies profile
resources, and starts the VM through the backend. The sandbox then walks the
state machine `REQUESTED -> PROVISIONING -> BOOTING -> READY -> RUNNING` and on
to a terminal state. See [the state machine reference](../reference/state-machine.md)
for every transition.

For a job, the orchestrator allocates a single-use bootstrap token and writes it
into the cloud-init snippet. When the VM boots, the in-guest `agent-runner`
fetches its payload, clones the repo, runs the agent, reports status, and uploads
artifacts. The full guest-side sequence is described in
[Guest runner flow](guest-runner-flow.md). Secrets never touch the VM disk; they
are delivered through one-time tokens into tmpfs, as explained in
[Secrets delivery model](secrets-delivery-model.md).

## Reconciliation and shutdown

A reconciler runs every 30 seconds to reconcile existing sandbox records
against Proxmox state, mark stale database entries destroyed, and clean up
zombie sandboxes. It reconciles known records and does not adopt orphaned VMs.
At shutdown, a task tracker drains in-flight background work before the store
closes. These loops are covered in
[Reconciliation and shutdown](reconciliation-and-shutdown.md).

## Where to go next

- For the route catalog: [HTTP API reference](../reference/http-api.md).
- For every config key: [Configuration reference](../reference/configuration.md).
- For the network model that surrounds the VMs:
  [Network isolation model](network-isolation-model.md).
- For a first end-to-end run: [Run your first job](../tutorials/run-first-job.md).
