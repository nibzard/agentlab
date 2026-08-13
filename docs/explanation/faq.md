# FAQ

Short answers to conceptual questions that span more than one page. Each links
to the page that goes deeper.

## Concepts

### What is the difference between a sandbox, a workspace, and a session?

A **sandbox** is an ephemeral VM with an ephemeral root disk. A **workspace**
is a persistent volume at `/work` that survives sandbox replacement. A
**session** binds a workspace to a profile and tracks the current sandbox. See
[Workspaces, sessions, and sandboxes](workspaces-sessions-sandboxes.md).

### Can AgentLab run without Proxmox?

No. AgentLab relies on Proxmox for VM cloning, storage, the QEMU guest agent,
and bridge networking. See [Why Proxmox](why-proxmox.md).

## Persistence and lifecycle

### What survives a sandbox destroy?

Only the workspace at `/work`. The sandbox root disk is reset on destroy or
revert. The session record and workspace binding also survive, so you can
resume. See [Workspaces, sessions, and sandboxes](workspaces-sessions-sandboxes.md).

### Why did my sandbox stop while I was away?

Idle auto-stop is enabled by default. After a period with no active SSH
session (detected through host conntrack) and CPU below a threshold, the
daemon transitions the sandbox to `STOPPED`. Set
`behavior.idle_stop_minutes_default: 0` in the profile to disable it. See
[Idle stop and lease model](idle-stop-and-lease-model.md).

### What happens when the daemon restarts?

Running sandbox VMs keep running in Proxmox. The reconciler re-adopts them on
the next cycle. New sandbox creation is unavailable only while the daemon is
down. See [Reconciliation and shutdown](reconciliation-and-shutdown.md).

## Backends

### Which Proxmox backend should I use?

The API backend is recommended for Proxmox VE 9.x or newer with a dedicated
API token. The shell backend is the code default and works on 8.x. See
[Shell versus API backend](shell-vs-api-backend.md).

### Why does `qm clone` fail with `ipcc_send_rec` errors?

The shell backend must reach the Proxmox IPC socket. systemd hardening
directives such as `NoNewPrivileges` and `PrivateTmp` block that path. Disable
them for the `agentlabd` unit, or switch to the API backend. See
[Shell versus API backend](shell-vs-api-backend.md).

## Capacity and performance

### How many sandboxes can I run at once?

Concurrency is bounded by host RAM first, then CPU. Memory should not be
overcommitted; CPU can be. Track usage against configured pool limits:

```bash
agentlab pool status
```

Concrete VM-per-core, memory, and concurrent-job guidance is not yet
documented and needs operator measurement.

### How do I tune provisioning and artifact limits?

Raise `provisioning_timeout` for slow storage or heavy cloud-init, and
`artifact_max_bytes` (default 256 MiB) for large bundles. See
[Performance and tuning](performance-and-tuning.md).

## Security

### Are artifacts encrypted at rest?

No. Artifacts are stored as plaintext under the artifact directory. Use
host-level encryption, or encrypt inside the VM before upload, if you need
confidentiality. See [Secrets reference](../reference/secrets.md).

### Can a sandbox reach my host network?

No. Sandboxes sit on the agent subnet with RFC1918 and IPv6 ULA egress
blocked, and host bind mounts are rejected at provisioning time. See
[Network isolation model](network-isolation-model.md).

### How are secrets delivered to a sandbox?

Secrets are encrypted at rest on the host, decrypted in memory by the daemon,
and delivered to the guest through a one-time bootstrap token into tmpfs, never
to the sandbox disk. See [Secrets delivery model](secrets-delivery-model.md).

## Upgrades and compatibility

### Do running sandboxes survive an upgrade?

Yes. Stop the daemon, swap the binaries, and start it again. Running VMs keep
running and are re-adopted by the reconciler. Back up the database and config
first. See [Upgrade and migrate](../how-to/upgrade-and-migrate.md).

### Are the API and event schemas stable across releases?

The contracts carry a `schema_version` exposed through `/v1/schema`. Specific
cross-release compatibility guarantees are not yet documented. Treat a newer
schema as not backwards-compatible with older binaries after a migration has
run. See [Upgrade and migrate](../how-to/upgrade-and-migrate.md).

## Optional components

### Is the SSH gateway released?

No. `agentlab-ssh-gateway` is built behind the `sshgateway` build tag and is
excluded from releases. The planned `ssh.gateway.*` event types are not
implemented. See [Build and run the SSH gateway](../how-to/build-and-run-ssh-gateway.md).

### Is there a web UI?

Yes, optionally. `agentlab-dashboard` proxies the browser to the daemon over
the Unix socket. A non-loopback bind requires a browser token and should sit
behind TLS or a trusted tunnel. See
[Run the dashboard](../how-to/run-the-dashboard.md).

## Where to go next

- Orientation: [Architecture](architecture.md).
- Troubleshooting: [Debug a stuck sandbox](../how-to/debug-a-stuck-sandbox.md).
- Commands: [CLI reference](../reference/cli.md).
