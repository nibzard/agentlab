# Performance and tuning

AgentLab performance is dominated by two things: how fast Proxmox can clone
and boot a VM, and how many sandboxes you can fit on a host at once. Most
tuning is therefore storage choice, profile sizing, and reclaim policy. This
page covers the knobs that matter and the metrics that tell you whether a
change helped.

!!! warning "No published benchmarks"
    AgentLab has no measured, verified benchmark numbers in its documentation.
    The provisioning, throughput, and sizing figures that circulate are
    operator estimates, not test results. Treat any specific number as
    illustrative until you measure it on your own host. Capacity sizing and
    benchmarks are a known documentation gap.

## Where time goes

A sandbox lifecycle has three cost centers:

1. **Provisioning.** Clone the template, write the cloud-init snippet, resize
   the root disk, and start the VM. This is storage-bound.
2. **Boot and guest agent.** The VM boots, the QEMU guest agent comes up, and
   the guest runner fetches its bootstrap payload.
3. **Job overhead.** Git clone, agent startup, and the final artifact upload.

The single biggest lever on provisioning time is the storage backend. ZFS
linked clones are effectively instant. Full copies on directory storage are
not. Volume snapshot, restore, and fork are supported on ZFS-backed storage
only.

## Configuration knobs

The timeouts bound how long the daemon waits before it gives up:

```yaml
proxmox_command_timeout: 2m    # one shell or API call
provisioning_timeout: 10m     # whole sandbox creation
artifact_max_bytes: 268435456 # 256 MiB upload cap
```

Raise `provisioning_timeout` when storage is slow or cloud-init installs many
packages. Raise `artifact_max_bytes` when jobs produce large bundles. Keep
`proxmox_command_timeout` low enough that a stuck Proxmox call fails fast.

Clone mode also matters:

```yaml
proxmox_clone_mode: linked   # fast, shares template backing
```

Prefer `linked` clones. Use `full` only when you need independent disks.

## Idle stop as a capacity tool

Idle stop is the most effective capacity control you have, because it reclaims
sandboxes that are doing nothing. The defaults stop a sandbox after 30 minutes
with no SSH session and CPU under 5 percent.

For high-churn automation, shorter idle windows reclaim resources faster. For
interactive development, raise the window or disable it per profile. See
[Idle stop and lease model](idle-stop-and-lease-model.md).

## Pool limits and over-commit

You can declare the physical budget for sandboxes so the daemon can report
over-commit:

```yaml
pool_total_cores: 32
pool_total_memory_mb: 131072
```

With both set to `0` (the default), the pool is unlimited and admission is
bounded only by what Proxmox will accept. Memory should not be overcommitted;
CPU can be. Check current usage with `agentlab pool status`.

!!! note "Admission control"
    The pool over-commit and admission-control policy, including the exact
    rejection behavior when limits are exceeded, is not yet documented. Treat
    `pool_total_*` as reporting knobs until the admission contract is
    documented.

## Daemon footprint

The daemon itself is light. It is a single Go process that talks to Proxmox
and a SQLite database. Idle CPU is negligible and resident memory is in the
tens of megabytes. Under load the cost is Proxmox I/O and API calls, not the
daemon. The SQLite database grows slowly; run `VACUUM` periodically on large
deployments.

## Observability

Turn on the metrics listener to see whether a tuning change helped. It must
bind to loopback:

```yaml
metrics_listen: "127.0.0.1:8847"
```

The metrics to watch are provisioning and start durations, the
`transitions_total` counter, and job time-to-start. These tell you which part
of the lifecycle to tune next. See
[Scrape Prometheus metrics](../how-to/scrape-prometheus-metrics.md) and the
[Prometheus metrics](../reference/metrics.md) reference.

## Where to go next

- Tune reclaim policy: [Idle stop and lease model](idle-stop-and-lease-model.md).
- Author right-sized profiles: [Author a profile](../how-to/author-a-profile.md).
- All tunable keys: [Configuration reference](../reference/configuration.md).
- Measure before you scale: [Scrape Prometheus metrics](../how-to/scrape-prometheus-metrics.md).
